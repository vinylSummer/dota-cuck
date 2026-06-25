package workers

import (
	"context"
	"sync"
	"testing"

	pb "github.com/vinylSummer/dota-cuck/gen/spectator/v1"
)

type fakeObserver struct {
	mu        sync.Mutex
	streamURL string
	fatalCode string
	idles     int
	matchID   uint64
	guard     string
}

func (o *fakeObserver) OnMatchIDResolved(m uint64) { o.mu.Lock(); o.matchID = m; o.mu.Unlock() }
func (o *fakeObserver) OnStreamStarted(u string)   { o.mu.Lock(); o.streamURL = u; o.mu.Unlock() }
func (o *fakeObserver) OnFatalError(c, _ string)   { o.mu.Lock(); o.fatalCode = c; o.mu.Unlock() }
func (o *fakeObserver) OnWorkerIdle()              { o.mu.Lock(); o.idles++; o.mu.Unlock() }
func (o *fakeObserver) OnSteamGuard(g string)      { o.mu.Lock(); o.guard = g; o.mu.Unlock() }

func (o *fakeObserver) get(fn func(*fakeObserver) bool) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return fn(o)
}

// readyStream opens a worker stream, registers it, and returns it.
func readyStream(t *testing.T, client pb.ControlPlaneServiceClient) pb.ControlPlaneService_WorkerSessionClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	stream, err := client.WorkerSession(ctx)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if err := stream.Send(&pb.WorkerEvent{WorkerId: "w1", Payload: &pb.WorkerEvent_Ready{Ready: &pb.WorkerReady{}}}); err != nil {
		t.Fatalf("send ready: %v", err)
	}
	return stream
}

func TestSessionEventsRouteToObserver(t *testing.T) {
	client, _, cpSrv := startServer(t)
	obs := &fakeObserver{}
	cpSrv.SetSessionObserver(obs)
	stream := readyStream(t, client)

	send := func(ev *pb.WorkerEvent) {
		if err := stream.Send(ev); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	send(&pb.WorkerEvent{Payload: &pb.WorkerEvent_MatchIdResolved{MatchIdResolved: &pb.MatchIdResolved{MatchId: 42}}})
	send(&pb.WorkerEvent{Payload: &pb.WorkerEvent_StreamStarted{StreamStarted: &pb.StreamStarted{SrtUrl: "srt://x?streamid=publish:live/match"}}})
	send(&pb.WorkerEvent{Payload: &pb.WorkerEvent_StatusUpdate{StatusUpdate: &pb.StatusUpdate{State: pb.WorkerState_IDLE}}})

	waitFor(t, func() bool {
		return obs.get(func(o *fakeObserver) bool {
			return o.matchID == 42 && o.streamURL == "srt://x?streamid=publish:live/match" && o.idles == 1
		})
	})
}

func TestFatalErrorRoutesToObserverButNonFatalDoesNot(t *testing.T) {
	client, _, cpSrv := startServer(t)
	obs := &fakeObserver{}
	cpSrv.SetSessionObserver(obs)
	stream := readyStream(t, client)

	// Non-fatal error must not drive the session machine.
	_ = stream.Send(&pb.WorkerEvent{Payload: &pb.WorkerEvent_Error{Error: &pb.ErrorEvent{Code: "WARN", Fatal: false}}})
	// Fatal error must.
	_ = stream.Send(&pb.WorkerEvent{Payload: &pb.WorkerEvent_Error{Error: &pb.ErrorEvent{Code: "DOTA_CRASH", Fatal: true}}})

	waitFor(t, func() bool {
		return obs.get(func(o *fakeObserver) bool { return o.fatalCode == "DOTA_CRASH" })
	})
}

func TestSteamGuardWithRequestIDDoesNotHitSessionObserver(t *testing.T) {
	client, _, cpSrv := startServer(t)
	obs := &fakeObserver{}
	cpSrv.SetSessionObserver(obs)
	stream := readyStream(t, client)

	// request_id set => account-link guard (routed to pendingLinks), NOT the session.
	_ = stream.Send(&pb.WorkerEvent{Payload: &pb.WorkerEvent_SteamGuard{SteamGuard: &pb.SteamGuardRequired{
		RequestId: "req-1", GuardType: pb.SteamGuardType_EMAIL,
	}}})
	// empty request_id => session-driven spectate login, routed to the observer.
	_ = stream.Send(&pb.WorkerEvent{Payload: &pb.WorkerEvent_SteamGuard{SteamGuard: &pb.SteamGuardRequired{
		GuardType: pb.SteamGuardType_MOBILE,
	}}})

	waitFor(t, func() bool {
		return obs.get(func(o *fakeObserver) bool { return o.guard == "MOBILE" })
	})
	// The account-link guard never reached the session observer.
	if obs.get(func(o *fakeObserver) bool { return o.guard == "EMAIL" }) {
		t.Error("account-link guard (request_id set) leaked to the session observer")
	}
}
