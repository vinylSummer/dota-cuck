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
  }
}

message WorkerReady        {}
message StatusUpdate       { WorkerState state = 1; }
message SteamGuardRequired { SteamGuardType guard_type = 1; }
message MatchIdResolved    { uint64 match_id = 1; string steam_id = 2; }
message StreamStarted      { string srt_url = 1; }
message ErrorEvent         { string code = 1; string message = 2; bool fatal = 3; }

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
  }
}

message StartSpectate {
  string session_id    = 1;
  string target_steam_id = 2;       // friend's Steam ID to spectate
  string steam_username  = 3;       // credentials decrypted in memory by control plane
  string steam_password  = 4;
  bytes  sentry_hash     = 5;       // device trust token if available; empty on first login
}

// Friends fetch. The worker serves this from its warm in-process python-steam
// session, connecting lazily (relogin via login_key when available, else
// credentials) on the first call, then replying with FriendsResult.
message ListFriends {
  string request_id     = 1;        // correlates the FriendsResult reply
  string steam_username  = 2;       // decrypted in memory by control plane
  string steam_password  = 3;
  bytes  sentry_hash     = 4;       // device trust token if available
}

message StopSpectate         {}
message SubmitSteamGuardCode { string code = 1; }

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
