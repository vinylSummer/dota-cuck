package api

// PushEvent is a server→client WebSocket message. The JSON shape is consumed
// by the frontend and must match the "WebSocket push events" section of
// CLAUDE.md exactly. omitempty keeps each event type to only its documented
// fields.
type PushEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	State     string `json:"state,omitempty"`
	GuardType string `json:"guard_type,omitempty"`
	WebRTCURL string `json:"webrtc_url,omitempty"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
}

// SessionStateEvent: { "type": "session_state", "session_id": ..., "state": ... }
func SessionStateEvent(sessionID, state string) PushEvent {
	return PushEvent{Type: "session_state", SessionID: sessionID, State: state}
}

// SteamGuardEvent: { "type": "steam_guard", "session_id": ..., "guard_type": ... }
func SteamGuardEvent(sessionID, guardType string) PushEvent {
	return PushEvent{Type: "steam_guard", SessionID: sessionID, GuardType: guardType}
}

// StreamReadyEvent: { "type": "stream_ready", "session_id": ..., "webrtc_url": ... }
func StreamReadyEvent(sessionID, webrtcURL string) PushEvent {
	return PushEvent{Type: "stream_ready", SessionID: sessionID, WebRTCURL: webrtcURL}
}

// ErrorEvent: { "type": "error", "session_id": ..., "code": ..., "message": ... }
func ErrorEvent(sessionID, code, message string) PushEvent {
	return PushEvent{Type: "error", SessionID: sessionID, Code: code, Message: message}
}
