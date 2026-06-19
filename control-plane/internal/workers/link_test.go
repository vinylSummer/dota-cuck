package workers

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/vinylSummer/dota-cuck/gen/spectator/v1"
)

// TestLinkRoundTripWithGuard: a worker connects, the control plane calls Link,
// the worker requests a Steam Guard code (correlated by request_id), the control
// plane relays the submitted code, and the worker replies LinkResult.
func TestLinkRoundTripWithGuard(t *testing.T) {
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

	// Fake worker: on LinkAccount, prompt Steam Guard; on the code, reply LinkResult.
	go func() {
		for {
			cmd, err := stream.Recv()
			if err != nil {
				return
			}
			switch p := cmd.GetPayload().(type) {
			case *pb.Command_LinkAccount:
				_ = stream.Send(&pb.WorkerEvent{WorkerId: "w1", Payload: &pb.WorkerEvent_SteamGuard{
					SteamGuard: &pb.SteamGuardRequired{RequestId: p.LinkAccount.GetRequestId(), GuardType: pb.SteamGuardType_EMAIL},
				}})
			case *pb.Command_SteamGuard:
				_ = stream.Send(&pb.WorkerEvent{WorkerId: "w1", Payload: &pb.WorkerEvent_LinkResult{
					LinkResult: &pb.LinkResult{RequestId: p.SteamGuard.GetRequestId(), OwnerSteamId: "76561198000000777"},
				}})
			}
		}
	}()

	var gotGuard atomic.Int32
	onGuard := func(gt pb.SteamGuardType) {
		if gt == pb.SteamGuardType_EMAIL {
			gotGuard.Add(1)
		}
		// Relay the user's code, which prompts the worker's LinkResult.
		_ = srv.SubmitGuardCode("req-link-1", "K4J9X")
	}

	res, err := srv.Link(ctx, "req-link-1", "alice_dota", "s3cr3t", onGuard, nil)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if res.GetOwnerSteamId() != "76561198000000777" {
		t.Errorf("owner = %q", res.GetOwnerSteamId())
	}
	if gotGuard.Load() != 1 {
		t.Errorf("guard callback fired %d times, want 1", gotGuard.Load())
	}
}

func TestLinkNoWorker(t *testing.T) {
	_, _, srv := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := srv.Link(ctx, "req-x", "u", "p", nil, nil); err != ErrNoWorker {
		t.Fatalf("Link with no worker = %v, want ErrNoWorker", err)
	}
}
