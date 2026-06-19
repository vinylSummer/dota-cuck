package workers

import (
	"context"
	"testing"
	"time"

	pb "github.com/vinylSummer/dota-cuck/gen/spectator/v1"
)

// TestListFriendsRoundTrip: a worker connects, the control plane calls
// ListFriends, the worker receives the command and replies with a correlated
// FriendsResult, and ListFriends returns it.
func TestListFriendsRoundTrip(t *testing.T) {
	client, reg, srv := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.WorkerSession(ctx)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if err := stream.Send(&pb.WorkerEvent{WorkerId: "w1", Payload: &pb.WorkerEvent_Ready{Ready: &pb.WorkerReady{}}}); err != nil {
		t.Fatalf("send ready: %v", err)
	}
	waitFor(t, func() bool { return reg.Current() != nil })

	// The fake worker: receive the ListFriends command, reply FriendsResult with
	// the same request_id.
	go func() {
		cmd, err := stream.Recv()
		if err != nil {
			return
		}
		lf := cmd.GetListFriends()
		if lf == nil {
			return
		}
		_ = stream.Send(&pb.WorkerEvent{
			WorkerId: "w1",
			Payload: &pb.WorkerEvent_FriendsResult{FriendsResult: &pb.FriendsResult{
				RequestId:    lf.GetRequestId(),
				OwnerSteamId: "76561198179568701",
				Friends: []*pb.Friend{
					{SteamId: "11", PersonaName: "zoe", Online: true, InMatch: true},
				},
			}},
		})
	}()

	res, err := srv.ListFriends(ctx, "refresh-tok")
	if err != nil {
		t.Fatalf("ListFriends: %v", err)
	}
	if res.GetOwnerSteamId() != "76561198179568701" {
		t.Errorf("owner = %q", res.GetOwnerSteamId())
	}
	if len(res.GetFriends()) != 1 || res.GetFriends()[0].GetPersonaName() != "zoe" {
		t.Fatalf("unexpected friends: %+v", res.GetFriends())
	}
}

func TestListFriendsNoWorker(t *testing.T) {
	_, _, srv := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := srv.ListFriends(ctx, "rt"); err != ErrNoWorker {
		t.Fatalf("ListFriends with no worker = %v, want ErrNoWorker", err)
	}
}

// With a worker connected but never replying, ListFriends honours the context
// deadline instead of blocking forever.
func TestListFriendsTimeout(t *testing.T) {
	client, reg, srv := startServer(t)
	streamCtx, streamCancel := context.WithCancel(context.Background())
	defer streamCancel()

	stream, err := client.WorkerSession(streamCtx)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if err := stream.Send(&pb.WorkerEvent{WorkerId: "w1", Payload: &pb.WorkerEvent_Ready{Ready: &pb.WorkerReady{}}}); err != nil {
		t.Fatalf("send ready: %v", err)
	}
	waitFor(t, func() bool { return reg.Current() != nil })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = srv.ListFriends(ctx, "rt") // worker never replies
	if err != context.DeadlineExceeded {
		t.Fatalf("ListFriends err = %v, want DeadlineExceeded", err)
	}
}
