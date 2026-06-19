package workers

import (
	"context"
	"sync"

	pb "github.com/vinylSummer/dota-cuck/gen/spectator/v1"
)

// pendingLinks correlates LinkAccount commands with their LinkResult replies by
// request_id, and routes the interim SteamGuardRequired (credentials path) and
// SteamQrChallenge (QR path) events to the caller's callbacks. All arrive
// asynchronously on the worker stream.
type pendingLinks struct {
	mu         sync.Mutex
	chans      map[string]chan *pb.LinkResult
	guards     map[string]func(pb.SteamGuardType)
	challenges map[string]func(string)
}

func newPendingLinks() *pendingLinks {
	return &pendingLinks{
		chans:      make(map[string]chan *pb.LinkResult),
		guards:     make(map[string]func(pb.SteamGuardType)),
		challenges: make(map[string]func(string)),
	}
}

func (p *pendingLinks) add(reqID string, ch chan *pb.LinkResult, onGuard func(pb.SteamGuardType), onChallenge func(string)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.chans[reqID] = ch
	if onGuard != nil {
		p.guards[reqID] = onGuard
	}
	if onChallenge != nil {
		p.challenges[reqID] = onChallenge
	}
}

func (p *pendingLinks) remove(reqID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.chans, reqID)
	delete(p.guards, reqID)
	delete(p.challenges, reqID)
}

// deliver routes a final LinkResult to its waiter (non-blocking; buffered chan).
func (p *pendingLinks) deliver(res *pb.LinkResult) {
	p.mu.Lock()
	ch, ok := p.chans[res.GetRequestId()]
	p.mu.Unlock()
	if ok {
		ch <- res
	}
}

// guard invokes the caller's Steam Guard callback for an in-flight link.
func (p *pendingLinks) guard(reqID string, guardType pb.SteamGuardType) {
	p.mu.Lock()
	cb, ok := p.guards[reqID]
	p.mu.Unlock()
	if ok {
		cb(guardType)
	}
}

// challenge invokes the caller's QR-challenge callback for an in-flight link.
func (p *pendingLinks) challenge(reqID, url string) {
	p.mu.Lock()
	cb, ok := p.challenges[reqID]
	p.mu.Unlock()
	if ok {
		cb(url)
	}
}

// Link sends a LinkAccount command to the connected worker and waits for the
// correlated LinkResult. Empty username/password starts a QR link (onChallenge
// receives the URL to render); credentials start the email/no-2FA path (onGuard
// fires when a code is required, relayed later via SubmitGuardCode). Returns
// ErrNoWorker if none is connected, or the context error on timeout.
func (s *Server) Link(ctx context.Context, reqID, username, password string, onGuard func(pb.SteamGuardType), onChallenge func(string)) (*pb.LinkResult, error) {
	ch := make(chan *pb.LinkResult, 1)
	s.links.add(reqID, ch, onGuard, onChallenge)
	defer s.links.remove(reqID)

	cmd := &pb.Command{Payload: &pb.Command_LinkAccount{LinkAccount: &pb.LinkAccount{
		RequestId:     reqID,
		SteamUsername: username,
		SteamPassword: password,
	}}}
	if err := s.reg.Send(cmd); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		return res, nil
	}
}

// SubmitGuardCode relays a Steam Guard code to the worker, correlated to the
// in-flight login by request_id.
func (s *Server) SubmitGuardCode(reqID, code string) error {
	return s.reg.Send(&pb.Command{Payload: &pb.Command_SteamGuard{SteamGuard: &pb.SubmitSteamGuardCode{
		RequestId: reqID,
		Code:      code,
	}}})
}
