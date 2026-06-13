// Canonical API/WS payloads, copied verbatim from the HTTP API + WebSocket
// sections of CLAUDE.md (and matching control-plane events.go / models.go).
// Tests reuse these so a contract drift is a single edit here. The WS strings
// are intentionally identical to what the Go internal/api WS-marshaling test
// asserts it produces — both sides check the same literal shape.

export const wsEvents = {
  sessionState: { type: 'session_state', session_id: 's1', state: 'WATCHING' },
  guardSession: { type: 'steam_guard', session_id: 's1', guard_type: 'EMAIL' },
  guardAccount: { type: 'steam_guard', account_id: 'a1', guard_type: 'EMAIL' },
  accountLinked: { type: 'account_linked', account_id: 'a1', steam_id: '76561198000000000' },
  streamReady: {
    type: 'stream_ready',
    session_id: 's1',
    webrtc_url: 'https://dota.example.com/webrtc/live/match',
  },
  errorSession: { type: 'error', session_id: 's1', code: 'DOTA_CRASH', message: 'crashed' },
  errorAccount: { type: 'error', account_id: 'a1', code: 'LINK_FAILED', message: 'bad creds' },
};

export const friendsList = [
  { steam_id: '76561198000000001', persona_name: 'bob', online: true, in_match: true },
  { steam_id: '76561198000000002', persona_name: 'carol', online: true, in_match: false },
  { steam_id: '76561198000000003', persona_name: 'dave', online: false, in_match: false },
];

export const loginResponse = { token: 'jwt.test.token' };

export const sessionResponse = {
  id: 'sess-1',
  state: 'STARTING',
  target_steam_id: '76561198000000001',
};
