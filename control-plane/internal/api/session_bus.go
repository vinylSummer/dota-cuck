package api

import "context"

// SessionBus adapts the WebSocket Hub to the session manager's Broadcaster: it
// turns lifecycle callbacks into the documented push events. It satisfies
// sessions.Broadcaster structurally (no import of that package needed here).
type SessionBus struct {
	hub *Hub
}

// NewSessionBus builds the broadcaster the session manager pushes through.
func NewSessionBus(hub *Hub) *SessionBus { return &SessionBus{hub: hub} }

func (b *SessionBus) SessionState(sessionID, state string) {
	b.hub.Broadcast(context.Background(), SessionStateEvent(sessionID, state))
}

func (b *SessionBus) StreamReady(sessionID, webrtcURL string) {
	b.hub.Broadcast(context.Background(), StreamReadyEvent(sessionID, webrtcURL))
}

func (b *SessionBus) SessionError(sessionID, code, message string) {
	b.hub.Broadcast(context.Background(), ErrorEvent(sessionID, code, message))
}

func (b *SessionBus) SteamGuard(sessionID, guardType string) {
	b.hub.Broadcast(context.Background(), SteamGuardEvent(sessionID, guardType))
}
