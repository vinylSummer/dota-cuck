package workers

import (
	"errors"
	"io"
	"log/slog"

	pb "github.com/vinylSummer/dota-cuck/gen/spectator/v1"
)

// Server implements pb.ControlPlaneServiceServer. It owns one bidi stream per
// connected worker.
type Server struct {
	pb.UnimplementedControlPlaneServiceServer
	reg     *Registry
	log     *slog.Logger
	pending *pendingFriends
	links   *pendingLinks
}

func NewServer(reg *Registry, log *slog.Logger) *Server {
	return &Server{reg: reg, log: log, pending: newPendingFriends(), links: newPendingLinks()}
}

// WorkerSession is the long-lived bidirectional stream. The worker pushes
// WorkerEvents; the control plane pushes Commands. For the skeleton, events are
// logged and the worker is (un)registered on Ready / stream end.
func (s *Server) WorkerSession(stream pb.ControlPlaneService_WorkerSessionServer) error {
	w := &Worker{send: make(chan *pb.Command, 8)}
	ctx := stream.Context()

	// Pump queued commands out to the worker until the stream closes.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case cmd := <-w.send:
				if err := stream.Send(cmd); err != nil {
					s.log.Warn("send command failed", "worker_id", w.ID, "err", err)
					return
				}
			}
		}
	}()

	defer s.reg.unregister(w)
	for {
		ev, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			s.log.Info("worker stream closed", "worker_id", w.ID)
			return nil
		}
		if err != nil {
			s.log.Warn("worker stream error", "worker_id", w.ID, "err", err)
			return err
		}
		s.handle(w, ev)
	}
}

func (s *Server) handle(w *Worker, ev *pb.WorkerEvent) {
	switch p := ev.GetPayload().(type) {
	case *pb.WorkerEvent_Ready:
		w.ID = ev.GetWorkerId()
		s.reg.register(w)
		s.log.Info("worker ready", "worker_id", w.ID)
	case *pb.WorkerEvent_StatusUpdate:
		s.log.Info("worker status", "worker_id", w.ID, "state", p.StatusUpdate.GetState())
	case *pb.WorkerEvent_SteamGuard:
		s.log.Info("steam guard required", "worker_id", w.ID,
			"type", p.SteamGuard.GetGuardType(), "request_id", p.SteamGuard.GetRequestId())
		s.links.guard(p.SteamGuard.GetRequestId(), p.SteamGuard.GetGuardType())
	case *pb.WorkerEvent_QrChallenge:
		s.log.Info("steam qr challenge", "worker_id", w.ID, "request_id", p.QrChallenge.GetRequestId())
		s.links.challenge(p.QrChallenge.GetRequestId(), p.QrChallenge.GetChallengeUrl())
	case *pb.WorkerEvent_MatchIdResolved:
		s.log.Info("match id resolved", "worker_id", w.ID, "match_id", p.MatchIdResolved.GetMatchId())
	case *pb.WorkerEvent_StreamStarted:
		s.log.Info("stream started", "worker_id", w.ID, "srt_url", p.StreamStarted.GetSrtUrl())
	case *pb.WorkerEvent_Error:
		s.log.Warn("worker error", "worker_id", w.ID,
			"code", p.Error.GetCode(), "message", p.Error.GetMessage(), "fatal", p.Error.GetFatal())
	case *pb.WorkerEvent_FriendsResult:
		s.pending.deliver(p.FriendsResult)
	case *pb.WorkerEvent_LinkResult:
		s.links.deliver(p.LinkResult)
	default:
		s.log.Warn("unknown worker event", "worker_id", w.ID)
	}
}
