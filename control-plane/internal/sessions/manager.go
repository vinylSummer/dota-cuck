package sessions

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Errors returned to the HTTP layer.
var (
	// ErrBusy means a session is already active (V1 is single-worker/single-session).
	ErrBusy = errors.New("sessions: a session is already active")
	// ErrNotFound means no active session with that id belongs to the caller.
	ErrNotFound = errors.New("sessions: session not found")
)

// Info is a snapshot of a session for the API layer. UserID scopes ownership and
// is not serialized.
type Info struct {
	ID            string
	UserID        string
	State         string
	TargetSteamID string
	MatchID       uint64
	WebRTCURL     string
}

// Commander sends spectate commands to the worker. The concrete implementation
// is the worker gRPC server; it returns an error if no worker is connected.
type Commander interface {
	StartSpectate(sessionID, targetSteamID, refreshToken string) error
	StopSpectate() error
	SubmitSpectateGuard(code string) error
}

// Broadcaster pushes session lifecycle events to connected WebSocket clients.
// The concrete implementation adapts the API hub.
type Broadcaster interface {
	SessionState(sessionID, state string)
	StreamReady(sessionID, webrtcURL string)
	SessionError(sessionID, code, message string)
	SteamGuard(sessionID, guardType string)
}

// Store persists session rows. Writes are best-effort: the in-memory state is
// authoritative for the live session, the DB is for durability/inspection.
type Store interface {
	Create(ctx context.Context, userID, targetSteamID string) (string, error)
	SetState(ctx context.Context, id, state string) error
	SetMatchID(ctx context.Context, id string, matchID uint64) error
	SetStream(ctx context.Context, id, webrtcURL string) error
	MarkEnded(ctx context.Context, id, state string) error
}

// Deps are the Manager's collaborators.
type Deps struct {
	Cmd        Commander
	Bus        Broadcaster
	Store      Store
	Log        *slog.Logger
	WebRTCBase string // e.g. https://dota.example.com; the WHEP URL is {base}/webrtc/{path}
}

// Manager drives the control-plane session lifecycle: it owns the single active
// session (V1), applies the pure state machine on HTTP actions and worker
// events, persists each transition, and pushes WS updates. It implements the
// worker SessionObserver (the On* methods).
type Manager struct {
	cmd        Commander
	bus        Broadcaster
	store      Store
	log        *slog.Logger
	webrtcBase string

	mu  sync.Mutex
	cur *Info
}

func NewManager(d Deps) *Manager {
	return &Manager{
		cmd:        d.Cmd,
		bus:        d.Bus,
		store:      d.Store,
		log:        d.Log,
		webrtcBase: d.WebRTCBase,
	}
}

// Start opens a session for userID against targetSteamID and sends StartSpectate
// to the worker. Returns ErrBusy if a session is already active, or the
// commander's error (e.g. no worker connected) — in which case no session is
// left active.
func (m *Manager) Start(ctx context.Context, userID, targetSteamID, refreshToken string) (*Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur != nil {
		return nil, ErrBusy
	}

	id, err := m.store.Create(ctx, userID, targetSteamID)
	if err != nil {
		return nil, err
	}

	// OFF --start--> STARTING (pure; cannot fail for this edge).
	next, _ := Next(StateOff, EventStart)

	if err := m.cmd.StartSpectate(id, targetSteamID, refreshToken); err != nil {
		// The command never reached a worker; close the row out so no session is
		// left dangling and the next Start is accepted.
		m.persist(func(c context.Context) error { return m.store.MarkEnded(c, id, string(StateOff)) })
		return nil, err
	}

	m.cur = &Info{ID: id, UserID: userID, State: string(next), TargetSteamID: targetSteamID}
	m.persist(func(c context.Context) error { return m.store.SetState(c, id, string(next)) })
	m.bus.SessionState(id, string(next))
	return m.snapshot(), nil
}

// Get returns the caller's active session by id.
func (m *Manager) Get(userID, id string) (*Info, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.owns(userID, id) {
		return nil, false
	}
	return m.snapshot(), true
}

// Stop requests teardown of the caller's active session (sends StopSpectate).
// Idempotent: stopping an already-stopping session is a no-op success.
func (m *Manager) Stop(userID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.owns(userID, id) {
		return ErrNotFound
	}
	next, err := Next(State(m.cur.State), EventStop)
	if err != nil {
		// Already STOPPING (the only state with no Stop edge while active) — no-op.
		return nil
	}
	if err := m.cmd.StopSpectate(); err != nil {
		return err
	}
	m.cur.State = string(next)
	m.persist(func(c context.Context) error { return m.store.SetState(c, id, string(next)) })
	m.bus.SessionState(id, string(next))
	return nil
}

// SubmitGuard relays a Steam Guard code for the caller's starting session.
func (m *Manager) SubmitGuard(userID, id, code string) error {
	m.mu.Lock()
	owns := m.owns(userID, id)
	m.mu.Unlock()
	if !owns {
		return ErrNotFound
	}
	return m.cmd.SubmitSpectateGuard(code)
}

// --- worker SessionObserver: events arriving on the gRPC stream ---

// OnMatchIDResolved records the resolved live match id on the active session.
func (m *Manager) OnMatchIDResolved(matchID uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		return
	}
	m.cur.MatchID = matchID
	id := m.cur.ID
	m.persist(func(c context.Context) error { return m.store.SetMatchID(c, id, matchID) })
}

// OnStreamStarted advances STARTING -> WATCHING, derives the WHEP URL from the
// worker's SRT URL, persists it, and pushes session_state + stream_ready.
func (m *Manager) OnStreamStarted(srtURL string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		return
	}
	next, err := Next(State(m.cur.State), EventStreamStarted)
	if err != nil {
		m.log.Warn("stream started in unexpected state", "state", m.cur.State)
		return
	}
	webrtc := webRTCURL(m.webrtcBase, srtURL)
	id := m.cur.ID
	m.cur.State = string(next)
	m.cur.WebRTCURL = webrtc
	m.persist(func(c context.Context) error { return m.store.SetStream(c, id, webrtc) })
	m.persist(func(c context.Context) error { return m.store.SetState(c, id, string(next)) })
	m.bus.SessionState(id, string(next))
	m.bus.StreamReady(id, webrtc)
}

// OnFatalError advances the active session to STOPPING and pushes the error.
// The worker then tears down and reports IDLE, handled by OnWorkerIdle.
func (m *Manager) OnFatalError(code, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		return
	}
	next, err := Next(State(m.cur.State), EventFatalError)
	if err != nil {
		return
	}
	id := m.cur.ID
	m.cur.State = string(next)
	m.persist(func(c context.Context) error { return m.store.SetState(c, id, string(next)) })
	m.bus.SessionError(id, code, message)
	m.bus.SessionState(id, string(next))
}

// OnWorkerIdle closes out a stopping session (STOPPING -> OFF). A worker IDLE in
// any other state is ignored (e.g. the worker reaching IDLE on boot).
func (m *Manager) OnWorkerIdle() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil || m.cur.State != string(StateStopping) {
		return
	}
	next, _ := Next(StateStopping, EventWorkerIdle) // OFF
	id := m.cur.ID
	m.persist(func(c context.Context) error { return m.store.MarkEnded(c, id, string(next)) })
	m.bus.SessionState(id, string(next))
	m.cur = nil
}

// OnSteamGuard pushes a session-scoped Steam Guard prompt to the client.
func (m *Manager) OnSteamGuard(guardType string) {
	m.mu.Lock()
	id := ""
	if m.cur != nil {
		id = m.cur.ID
	}
	m.mu.Unlock()
	if id == "" {
		return
	}
	m.bus.SteamGuard(id, guardType)
}

// --- helpers (callers hold m.mu unless noted) ---

func (m *Manager) owns(userID, id string) bool {
	return m.cur != nil && m.cur.ID == id && m.cur.UserID == userID
}

func (m *Manager) snapshot() *Info {
	c := *m.cur
	return &c
}

// persist runs a best-effort store write off the request path with a short
// timeout; failures are logged, not surfaced (in-memory state is authoritative).
func (m *Manager) persist(fn func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fn(ctx); err != nil {
		m.log.Warn("session store write failed", "err", err)
	}
}

// webRTCURL turns the worker's SRT publish URL into the browser WHEP URL:
// srt://host:port?streamid=publish:live/match -> {base}/webrtc/live/match.
func webRTCURL(base, srtURL string) string {
	return strings.TrimRight(base, "/") + "/webrtc/" + streamPath(srtURL)
}

// streamPath extracts the mediamtx path from an SRT URL's streamid, stripping the
// publish: prefix. Falls back to the V1 single path if it can't be parsed.
func streamPath(srtURL string) string {
	const def = "live/match"
	i := strings.Index(srtURL, "streamid=")
	if i < 0 {
		return def
	}
	s := srtURL[i+len("streamid="):]
	if amp := strings.IndexByte(s, '&'); amp >= 0 {
		s = s[:amp]
	}
	s = strings.TrimPrefix(s, "publish:")
	if s == "" {
		return def
	}
	return s
}
