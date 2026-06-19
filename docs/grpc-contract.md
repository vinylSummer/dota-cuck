# gRPC Contract

Source of truth: **`proto/spectator/v1/worker.proto`**. Generated code lives in
`control-plane/gen/` (Go) and `worker/gen/` (Python); regenerate with `make proto`.

The control plane is the gRPC **server**; workers are gRPC **clients**. Each worker
opens a single long-lived `WorkerSession` bidirectional stream on startup. The
control plane pushes `Command` messages down at any time; the worker pushes
`WorkerEvent` messages up. Request/response pairs (e.g. ListFriends → FriendsResult)
are correlated by `request_id` over the one stream.

```protobuf
syntax = "proto3";
package spectator.v1;

option go_package = "github.com/youruser/dota-spectator/gen/spectator/v1";

service ControlPlaneService {
  // Worker opens this on startup. Control plane pushes Commands; worker pushes Events.
  rpc WorkerSession(stream WorkerEvent) returns (stream Command);
}

// === Events: Worker → Control Plane ===

message WorkerEvent {
  string worker_id = 1;
  oneof payload {
    WorkerReady        ready             = 2;
    StatusUpdate       status_update     = 3;
    SteamGuardRequired steam_guard       = 4;
    MatchIdResolved    match_id_resolved = 5;
    StreamStarted      stream_started    = 6;
    ErrorEvent         error             = 7;
    FriendsResult      friends_result    = 8;
    LinkResult         link_result       = 9;
    SteamQrChallenge   qr_challenge      = 10;
  }
}

message WorkerReady        {}
message StatusUpdate       { WorkerState state = 1; }
// request_id correlates the prompt with the LinkAccount/ListFriends/StartSpectate
// login that raised it; empty for a session-driven spectate login in V1.
message SteamGuardRequired { SteamGuardType guard_type = 1; string request_id = 2; }
message MatchIdResolved    { uint64 match_id = 1; string steam_id = 2; }
// Emitted during a QR account link: the worker started an IAuthenticationService
// QR session and the user must scan challenge_url with the Steam mobile app. A
// rotated URL is sent as a fresh SteamQrChallenge with the same request_id.
message SteamQrChallenge   { string request_id = 1; string challenge_url = 2; }
message StreamStarted      { string srt_url = 1; }
message ErrorEvent         { string code = 1; string message = 2; bool fatal = 3; }

// Response to a LinkAccount command. Correlated by request_id. On success,
// `owner_steam_id` is the account's own Steam ID (backfills steam_accounts) and
// `refresh_token` is the long-lived Steam refresh token the control plane
// encrypts and persists for zero-interaction cold logins; on failure `error` is set.
message LinkResult {
  string     request_id     = 1;
  string     owner_steam_id = 2;
  ErrorEvent error          = 3;
  string     refresh_token  = 4;
}

// Response to a ListFriends command. Correlated by request_id. On failure,
// `error` is set and `friends` is empty. `owner_steam_id` is the logged-in
// account's own Steam ID, used to backfill steam_accounts.steam_id.
message FriendsResult {
  string     request_id     = 1;
  repeated Friend friends    = 2;
  string     owner_steam_id  = 3;
  ErrorEvent error           = 4;
}

message Friend {
  string steam_id     = 1;
  string persona_name = 2;
  bool   online       = 3;
  bool   in_match     = 4;   // currently in a Dota 2 game
}

// === Commands: Control Plane → Worker ===

message Command {
  oneof payload {
    StartSpectate        start_spectate  = 1;
    StopSpectate         stop_spectate   = 2;
    SubmitSteamGuardCode steam_guard     = 3;
    ListFriends          list_friends    = 4;
    LinkAccount          link_account    = 5;
  }
}

// Standalone login to acquire a Steam refresh token (modern auth model) and
// report the account's own Steam ID, replying with LinkResult correlated by
// request_id. Two acquisition modes:
//   - QR (steam_username/steam_password empty): the worker opens an
//     IAuthenticationService QR session and emits SteamQrChallenge events with
//     the URL to scan; works for accounts with the Steam Mobile Authenticator.
//   - Credentials (both set): for email-only / no-2FA accounts that can't scan
//     a QR. The worker RSA-encrypts the password to Steam and, when an emailed
//     code is required, drives the interactive Steam Guard flow. Credentials are
//     decrypted in memory by the control plane and never persisted.
message LinkAccount {
  string request_id     = 1;
  string steam_username  = 2;       // empty => QR mode
  string steam_password  = 3;       // empty => QR mode
}

message StartSpectate {
  string session_id    = 1;
  string target_steam_id = 2;       // friend's Steam ID to spectate
  string refresh_token   = 3;       // decrypted in memory by control plane
  // reserved 4, 5;                  // was steam_password, sentry_hash
}

// Friends fetch. The worker serves this from its warm in-process python-steam
// session, logging onto the CM with the refresh token, then replying FriendsResult.
message ListFriends {
  string request_id     = 1;        // correlates the FriendsResult reply
  string refresh_token   = 2;       // decrypted in memory by control plane
  // reserved 3, 4;                  // was steam_password, sentry_hash
}

message StopSpectate         {}
// request_id correlates with the SteamGuardRequired prompt; empty for a
// session-driven spectate login in V1.
message SubmitSteamGuardCode { string code = 1; string request_id = 2; }

enum WorkerState {
  WORKER_STATE_UNSPECIFIED = 0;
  STOPPED    = 1;
  STARTING   = 2;
  IDLE       = 3;
  SPECTATING = 4;
  STOPPING   = 5;
}

enum SteamGuardType {
  STEAM_GUARD_TYPE_UNSPECIFIED = 0;
  EMAIL  = 1;
  MOBILE = 2;
}
```

## HTTP API documentation (swaggo)

The HTTP API is documented with **swaggo** (code-first). Each handler in
`control-plane/internal/api/handlers.go` carries `// @...` annotations; request and
response shapes are the DTO structs in `internal/api/models.go`. General API info
lives above `main()` in `cmd/server/main.go`.

- Regenerate after changing annotations or DTOs: `make docs` (runs `swag init` →
  `control-plane/docs/`). The generated `docs/` package is **committed** so the binary
  builds without the swag CLI; `main.go` blank-imports it.
- Served at **`/docs`** (Swagger UI); raw spec at `/docs/doc.json`.
- swaggo emits Swagger 2.0; the spec's `basePath` is `/api`.
