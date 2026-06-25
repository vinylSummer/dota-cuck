package sessions

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

// --- fakes ---

type fakeCmd struct {
	startErr   error
	stopErr    error
	startCalls []string // "id|target|token"
	stops      int
	guards     []string
}

func (f *fakeCmd) StartSpectate(id, target, token string) error {
	f.startCalls = append(f.startCalls, id+"|"+target+"|"+token)
	return f.startErr
}
func (f *fakeCmd) StopSpectate() error                { f.stops++; return f.stopErr }
func (f *fakeCmd) SubmitSpectateGuard(c string) error { f.guards = append(f.guards, c); return nil }

type fakeBus struct{ events []string }

func (b *fakeBus) SessionState(id, state string)     { b.events = append(b.events, "state:"+state) }
func (b *fakeBus) StreamReady(id, url string)        { b.events = append(b.events, "stream:"+url) }
func (b *fakeBus) SessionError(id, code, msg string) { b.events = append(b.events, "error:"+code) }
func (b *fakeBus) SteamGuard(id, guardType string)   { b.events = append(b.events, "guard:"+guardType) }

type fakeStore struct {
	createErr error
	states    []string
	ended     []string
	matchID   uint64
	stream    string
}

func (s *fakeStore) Create(_ context.Context, _, _ string) (string, error) {
	return "sess-1", s.createErr
}
func (s *fakeStore) SetState(_ context.Context, _, st string) error {
	s.states = append(s.states, st)
	return nil
}
func (s *fakeStore) SetMatchID(_ context.Context, _ string, m uint64) error {
	s.matchID = m
	return nil
}
func (s *fakeStore) SetStream(_ context.Context, _, url string) error { s.stream = url; return nil }
func (s *fakeStore) MarkEnded(_ context.Context, _, st string) error {
	s.ended = append(s.ended, st)
	return nil
}

func newManager(cmd *fakeCmd, bus *fakeBus, st *fakeStore) *Manager {
	return NewManager(Deps{
		Cmd: cmd, Bus: bus, Store: st,
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		WebRTCBase: "https://dota.example.com",
	})
}

// --- happy path: full lifecycle OFF->STARTING->WATCHING->STOPPING->OFF ---

func TestStartAdvancesToStartingAndSendsCommand(t *testing.T) {
	cmd, bus, st := &fakeCmd{}, &fakeBus{}, &fakeStore{}
	m := newManager(cmd, bus, st)

	info, err := m.Start(context.Background(), "user-1", "76561198000000001", "tok")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if info.State != string(StateStarting) {
		t.Errorf("state = %s, want STARTING", info.State)
	}
	if len(cmd.startCalls) != 1 || cmd.startCalls[0] != "sess-1|76561198000000001|tok" {
		t.Errorf("StartSpectate calls = %v", cmd.startCalls)
	}
	if len(bus.events) != 1 || bus.events[0] != "state:STARTING" {
		t.Errorf("events = %v", bus.events)
	}
}

func TestSecondStartIsBusy(t *testing.T) {
	m := newManager(&fakeCmd{}, &fakeBus{}, &fakeStore{})
	if _, err := m.Start(context.Background(), "user-1", "t1", "tok"); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if _, err := m.Start(context.Background(), "user-2", "t2", "tok"); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Start err = %v, want ErrBusy", err)
	}
}

func TestStartCommandFailureLeavesNoSession(t *testing.T) {
	cmd := &fakeCmd{startErr: errors.New("no worker")}
	st := &fakeStore{}
	m := newManager(cmd, &fakeBus{}, st)

	if _, err := m.Start(context.Background(), "user-1", "t1", "tok"); err == nil {
		t.Fatal("Start: want error")
	}
	// The row was closed out and no session is active (a retry is accepted).
	if len(st.ended) != 1 || st.ended[0] != string(StateOff) {
		t.Errorf("ended = %v, want [OFF]", st.ended)
	}
	cmd.startErr = nil
	if _, err := m.Start(context.Background(), "user-1", "t1", "tok"); err != nil {
		t.Fatalf("retry Start: %v", err)
	}
}

func TestStreamStartedAdvancesToWatchingWithWebRTCURL(t *testing.T) {
	cmd, bus, st := &fakeCmd{}, &fakeBus{}, &fakeStore{}
	m := newManager(cmd, bus, st)
	info, _ := m.Start(context.Background(), "user-1", "t1", "tok")

	m.OnStreamStarted("srt://mediamtx:8890?streamid=publish:live/match")

	got, ok := m.Get("user-1", info.ID)
	if !ok || got.State != string(StateWatching) {
		t.Fatalf("state = %v (ok=%v), want WATCHING", got, ok)
	}
	want := "https://dota.example.com/webrtc/live/match"
	if got.WebRTCURL != want {
		t.Errorf("webrtc = %q, want %q", got.WebRTCURL, want)
	}
	if st.stream != want {
		t.Errorf("persisted stream = %q, want %q", st.stream, want)
	}
	// session_state WATCHING and stream_ready both pushed.
	assertHas(t, bus.events, "state:WATCHING")
	assertHas(t, bus.events, "stream:"+want)
}

func TestStopAdvancesToStoppingThenWorkerIdleClosesIt(t *testing.T) {
	cmd, bus, st := &fakeCmd{}, &fakeBus{}, &fakeStore{}
	m := newManager(cmd, bus, st)
	info, _ := m.Start(context.Background(), "user-1", "t1", "tok")
	m.OnStreamStarted("srt://h:1?streamid=publish:live/match")

	if err := m.Stop("user-1", info.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if cmd.stops != 1 {
		t.Errorf("StopSpectate calls = %d, want 1", cmd.stops)
	}
	got, _ := m.Get("user-1", info.ID)
	if got.State != string(StateStopping) {
		t.Errorf("state = %s, want STOPPING", got.State)
	}

	m.OnWorkerIdle() // STOPPING -> OFF, session cleared
	if _, ok := m.Get("user-1", info.ID); ok {
		t.Error("session should be cleared after worker idle")
	}
	if len(st.ended) == 0 || st.ended[len(st.ended)-1] != string(StateOff) {
		t.Errorf("ended = %v, want last OFF", st.ended)
	}
}

func TestFatalErrorMovesToStoppingAndPushesError(t *testing.T) {
	cmd, bus, st := &fakeCmd{}, &fakeBus{}, &fakeStore{}
	m := newManager(cmd, bus, st)
	info, _ := m.Start(context.Background(), "user-1", "t1", "tok")

	m.OnFatalError("DOTA_CRASH", "boom")

	got, _ := m.Get("user-1", info.ID)
	if got.State != string(StateStopping) {
		t.Errorf("state = %s, want STOPPING", got.State)
	}
	assertHas(t, bus.events, "error:DOTA_CRASH")
	assertHas(t, bus.events, "state:STOPPING")
}

func TestWorkerIdleIgnoredWhenNotStopping(t *testing.T) {
	m := newManager(&fakeCmd{}, &fakeBus{}, &fakeStore{})
	info, _ := m.Start(context.Background(), "user-1", "t1", "tok") // STARTING

	m.OnWorkerIdle() // not STOPPING -> ignored

	if got, ok := m.Get("user-1", info.ID); !ok || got.State != string(StateStarting) {
		t.Errorf("session should remain STARTING, got %v (ok=%v)", got, ok)
	}
}

func TestMatchIDResolvedRecorded(t *testing.T) {
	st := &fakeStore{}
	m := newManager(&fakeCmd{}, &fakeBus{}, st)
	info, _ := m.Start(context.Background(), "user-1", "t1", "tok")

	m.OnMatchIDResolved(29885347581173389)

	got, _ := m.Get("user-1", info.ID)
	if got.MatchID != 29885347581173389 || st.matchID != 29885347581173389 {
		t.Errorf("match id = %d / %d, want 29885347581173389", got.MatchID, st.matchID)
	}
}

func TestSteamGuardPushedForActiveSession(t *testing.T) {
	bus := &fakeBus{}
	m := newManager(&fakeCmd{}, bus, &fakeStore{})
	m.Start(context.Background(), "user-1", "t1", "tok")

	m.OnSteamGuard("EMAIL")
	assertHas(t, bus.events, "guard:EMAIL")
}

// --- ownership ---

func TestGetAndStopRejectOtherUsers(t *testing.T) {
	m := newManager(&fakeCmd{}, &fakeBus{}, &fakeStore{})
	info, _ := m.Start(context.Background(), "owner", "t1", "tok")

	if _, ok := m.Get("intruder", info.ID); ok {
		t.Error("Get should reject a non-owner")
	}
	if err := m.Stop("intruder", info.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Stop by non-owner err = %v, want ErrNotFound", err)
	}
	if err := m.SubmitGuard("intruder", info.ID, "1234"); !errors.Is(err, ErrNotFound) {
		t.Errorf("SubmitGuard by non-owner err = %v, want ErrNotFound", err)
	}
}

func TestStopUnknownSessionNotFound(t *testing.T) {
	m := newManager(&fakeCmd{}, &fakeBus{}, &fakeStore{})
	if err := m.Stop("user-1", "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Stop unknown err = %v, want ErrNotFound", err)
	}
}

// --- observer events with no active session are no-ops ---

func TestObserverEventsWithoutSessionAreNoops(t *testing.T) {
	bus := &fakeBus{}
	m := newManager(&fakeCmd{}, bus, &fakeStore{})
	m.OnStreamStarted("srt://h:1?streamid=publish:live/match")
	m.OnFatalError("X", "y")
	m.OnWorkerIdle()
	m.OnMatchIDResolved(1)
	m.OnSteamGuard("EMAIL")
	if len(bus.events) != 0 {
		t.Errorf("events = %v, want none", bus.events)
	}
}

// --- pure URL derivation ---

func TestStreamPath(t *testing.T) {
	cases := map[string]string{
		"srt://mediamtx:8890?streamid=publish:live/match": "live/match",
		"srt://h:1?streamid=live/match":                   "live/match",
		"srt://h:1?streamid=publish:live/match&latency=5": "live/match",
		"srt://h:1": "live/match", // fallback
		"garbage":   "live/match",
	}
	for in, want := range cases {
		if got := streamPath(in); got != want {
			t.Errorf("streamPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWebRTCURLTrimsBaseSlash(t *testing.T) {
	got := webRTCURL("https://dota.example.com/", "srt://h:1?streamid=publish:live/match")
	if got != "https://dota.example.com/webrtc/live/match" {
		t.Errorf("webRTCURL = %q", got)
	}
}

func assertHas(t *testing.T, events []string, want string) {
	t.Helper()
	for _, e := range events {
		if e == want {
			return
		}
	}
	t.Errorf("events %v missing %q", events, want)
}
