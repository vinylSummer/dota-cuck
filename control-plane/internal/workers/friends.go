package workers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"

	pb "github.com/vinylSummer/dota-cuck/gen/spectator/v1"
)

// pendingFriends correlates ListFriends commands with their FriendsResult
// replies by request_id. The reply arrives asynchronously on the worker stream;
// the waiting caller is woken via its channel.
type pendingFriends struct {
	mu    sync.Mutex
	chans map[string]chan *pb.FriendsResult
}

func newPendingFriends() *pendingFriends {
	return &pendingFriends{chans: make(map[string]chan *pb.FriendsResult)}
}

func (p *pendingFriends) add(reqID string, ch chan *pb.FriendsResult) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.chans[reqID] = ch
}

func (p *pendingFriends) remove(reqID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.chans, reqID)
}

// deliver routes a reply to its waiter (non-blocking; the channel is buffered).
func (p *pendingFriends) deliver(res *pb.FriendsResult) {
	p.mu.Lock()
	ch, ok := p.chans[res.GetRequestId()]
	p.mu.Unlock()
	if ok {
		ch <- res
	}
}

// ListFriends sends a ListFriends command to the connected worker and waits for
// the correlated FriendsResult. The worker logs onto the CM with the refresh
// token (decrypted in memory by the control plane). Returns ErrNoWorker if none
// is connected, or the context error on timeout/cancellation.
func (s *Server) ListFriends(ctx context.Context, refreshToken string) (*pb.FriendsResult, error) {
	reqID, err := newRequestID()
	if err != nil {
		return nil, err
	}

	ch := make(chan *pb.FriendsResult, 1)
	s.pending.add(reqID, ch)
	defer s.pending.remove(reqID)

	cmd := &pb.Command{Payload: &pb.Command_ListFriends{ListFriends: &pb.ListFriends{
		RequestId:    reqID,
		RefreshToken: refreshToken,
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

func newRequestID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("workers: request id: %w", err)
	}
	return hex.EncodeToString(b), nil
}
