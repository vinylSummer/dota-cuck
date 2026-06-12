package workers

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	pb "github.com/vinylSummer/dota-cuck/gen/spectator/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// startServer spins the gRPC server on an in-memory bufconn listener and
// returns a connected client stub plus the registry under test.
func startServer(t *testing.T) (pb.ControlPlaneServiceClient, *Registry) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	reg := NewRegistry()
	srv := grpc.NewServer()
	pb.RegisterControlPlaneServiceServer(srv, NewServer(reg, slog.New(slog.NewTextHandler(io.Discard, nil))))
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() {
		conn.Close()
		srv.Stop()
	})
	return pb.NewControlPlaneServiceClient(conn), reg
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

func TestWorkerReadyRegistersWorker(t *testing.T) {
	client, reg := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.WorkerSession(ctx)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if err := stream.Send(&pb.WorkerEvent{WorkerId: "w1", Payload: &pb.WorkerEvent_Ready{Ready: &pb.WorkerReady{}}}); err != nil {
		t.Fatalf("send ready: %v", err)
	}

	waitFor(t, func() bool {
		w := reg.Current()
		return w != nil && w.ID == "w1"
	})
}

func TestPushedCommandReachesWorker(t *testing.T) {
	client, reg := startServer(t)
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

	cmd := &pb.Command{Payload: &pb.Command_StartSpectate{StartSpectate: &pb.StartSpectate{SessionId: "s1", TargetSteamId: "76561"}}}
	if err := reg.Send(cmd); err != nil {
		t.Fatalf("registry send: %v", err)
	}

	got, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv command: %v", err)
	}
	if got.GetStartSpectate().GetSessionId() != "s1" {
		t.Fatalf("got session_id %q, want s1", got.GetStartSpectate().GetSessionId())
	}
}

func TestSendWithoutWorkerErrors(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Send(&pb.Command{}); err != ErrNoWorker {
		t.Fatalf("Send with no worker = %v, want ErrNoWorker", err)
	}
}
