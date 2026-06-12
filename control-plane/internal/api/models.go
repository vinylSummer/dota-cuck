package api

// Request/response DTOs for the HTTP API. Shapes follow the HTTP API section
// of CLAUDE.md. They double as the OpenAPI schema source (swaggo reads these)
// and as the types the handlers will marshal/unmarshal once implemented in
// later steps.

// RegisterRequest is the body of POST /api/auth/register.
type RegisterRequest struct {
	Username string `json:"username" example:"alice"`
	Password string `json:"password" example:"hunter2"`
}

// LoginRequest is the body of POST /api/auth/login.
type LoginRequest struct {
	Username string `json:"username" example:"alice"`
	Password string `json:"password" example:"hunter2"`
}

// LoginResponse is returned by POST /api/auth/login.
type LoginResponse struct {
	Token string `json:"token" example:"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."`
}

// SteamAccountRequest is the body of POST /api/steam/accounts.
type SteamAccountRequest struct {
	SteamUsername string `json:"steam_username" example:"alice_dota"`
	SteamPassword string `json:"steam_password" example:"s3cr3t"`
}

// SteamAccount is a linked Steam account as returned to the client. The Steam
// password is never returned.
type SteamAccount struct {
	ID            string `json:"id" example:"018f8c2e-1d3a-7c1b-9e2a-0a1b2c3d4e5f"`
	SteamID       string `json:"steam_id" example:"76561198000000000"`
	SteamUsername string `json:"steam_username" example:"alice_dota"`
	CreatedAt     string `json:"created_at" example:"2026-06-12T10:00:00Z"`
}

// Friend is one entry in the friends list with live status.
type Friend struct {
	SteamID     string `json:"steam_id" example:"76561198000000001"`
	PersonaName string `json:"persona_name" example:"bob"`
	Online      bool   `json:"online" example:"true"`
	InMatch     bool   `json:"in_match" example:"false"`
}

// SessionRequest is the body of POST /api/sessions.
type SessionRequest struct {
	TargetSteamID string `json:"target_steam_id" example:"76561198000000001"`
}

// Session is the status of a spectator session. WebRTCURL is set once the
// stream is ready (state WATCHING).
type Session struct {
	ID            string `json:"id" example:"018f8c2e-1d3a-7c1b-9e2a-0a1b2c3d4e5f"`
	State         string `json:"state" example:"WATCHING" enums:"OFF,STARTING,WATCHING,STOPPING"`
	TargetSteamID string `json:"target_steam_id" example:"76561198000000001"`
	MatchID       uint64 `json:"match_id,omitempty" example:"7654321098"`
	WebRTCURL     string `json:"webrtc_url,omitempty" example:"https://dota.example.com/webrtc/live/match"`
}

// SteamGuardRequest is the body of POST /api/sessions/{id}/steamguard.
type SteamGuardRequest struct {
	Code string `json:"code" example:"K4J9X"`
}

// ErrorResponse is the body returned for 4xx/5xx errors.
type ErrorResponse struct {
	Error string `json:"error" example:"not implemented"`
}
