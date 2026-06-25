package workers

import (
	pb "github.com/vinylSummer/dota-cuck/gen/spectator/v1"
)

// SessionObserver receives the session-lifecycle worker events (those not
// correlated by request_id). The control-plane session manager implements it;
// the worker stream handler forwards events to the registered observer. All
// arguments are primitives so the observer stays decoupled from the proto types.
type SessionObserver interface {
	OnMatchIDResolved(matchID uint64)
	OnStreamStarted(srtURL string)
	OnFatalError(code, message string)
	OnWorkerIdle()
	OnSteamGuard(guardType string)
}

// SetSessionObserver registers the observer for session-lifecycle events. Wired
// once at startup; not safe to call concurrently with active streams.
func (s *Server) SetSessionObserver(o SessionObserver) { s.obs = o }

// StartSpectate sends a StartSpectate command to the connected worker.
func (s *Server) StartSpectate(sessionID, targetSteamID, refreshToken string) error {
	return s.reg.Send(&pb.Command{Payload: &pb.Command_StartSpectate{StartSpectate: &pb.StartSpectate{
		SessionId:     sessionID,
		TargetSteamId: targetSteamID,
		RefreshToken:  refreshToken,
	}}})
}

// StopSpectate sends a StopSpectate command to the connected worker.
func (s *Server) StopSpectate() error {
	return s.reg.Send(&pb.Command{Payload: &pb.Command_StopSpectate{StopSpectate: &pb.StopSpectate{}}})
}

// SubmitSpectateGuard relays a Steam Guard code for the in-flight spectate login.
// V1 has a single in-flight login, so no request id is needed.
func (s *Server) SubmitSpectateGuard(code string) error {
	return s.reg.Send(&pb.Command{Payload: &pb.Command_SteamGuard{SteamGuard: &pb.SubmitSteamGuardCode{Code: code}}})
}

// guardTypeString maps the proto Steam Guard enum to the WebSocket guard_type.
func guardTypeString(gt pb.SteamGuardType) string {
	switch gt {
	case pb.SteamGuardType_EMAIL:
		return "EMAIL"
	case pb.SteamGuardType_MOBILE:
		return "MOBILE"
	default:
		return ""
	}
}
