// WebSocket push channel. routeEvent is the pure dispatcher — it classifies a
// raw server event by `type` and discriminates the account- vs session-scoped
// variants of steam_guard/error by which id is present. The socket lifecycle
// and pub/sub around it are glue. Event shapes match control-plane events.go.

// routeEvent normalizes a raw push event into { kind, scope, id, ... } or null
// for anything unrecognized (ignored, never thrown).
export function routeEvent(ev) {
  if (!ev || typeof ev.type !== 'string') return null;
  switch (ev.type) {
    case 'session_state':
      return { kind: 'session_state', scope: 'session', id: ev.session_id, state: ev.state };
    case 'stream_ready':
      return { kind: 'stream_ready', scope: 'session', id: ev.session_id, webrtcUrl: ev.webrtc_url };
    case 'account_linked':
      return { kind: 'account_linked', scope: 'account', id: ev.account_id, steamId: ev.steam_id };
    case 'steam_qr':
      return { kind: 'steam_qr', scope: 'account', id: ev.account_id, challengeUrl: ev.challenge_url };
    case 'steam_guard':
      return ev.account_id
        ? { kind: 'steam_guard', scope: 'account', id: ev.account_id, guardType: ev.guard_type }
        : { kind: 'steam_guard', scope: 'session', id: ev.session_id, guardType: ev.guard_type };
    case 'error':
      return ev.account_id
        ? { kind: 'error', scope: 'account', id: ev.account_id, code: ev.code, message: ev.message }
        : { kind: 'error', scope: 'session', id: ev.session_id, code: ev.code, message: ev.message };
    default:
      return null;
  }
}

const listeners = new Set();
let socket = null;

// connect opens the single app WebSocket (idempotent) and fans routed events out
// to subscribers. The control plane currently authenticates the WS by origin,
// not a token, so no credentials are attached here.
export function connect() {
  if (socket) return socket;
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  socket = new WebSocket(`${proto}://${location.host}/ws`);
  socket.addEventListener('message', (e) => {
    let raw;
    try {
      raw = JSON.parse(e.data);
    } catch {
      return;
    }
    const ev = routeEvent(raw);
    if (ev) listeners.forEach((fn) => fn(ev));
  });
  socket.addEventListener('close', () => {
    socket = null;
  });
  return socket;
}

export function disconnect() {
  if (socket) {
    socket.close();
    socket = null;
  }
}

// subscribe registers a handler for routed events and returns an unsubscribe fn.
export function subscribe(fn) {
  listeners.add(fn);
  return () => listeners.delete(fn);
}
