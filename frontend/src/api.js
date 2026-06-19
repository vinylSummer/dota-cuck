// Thin HTTP client over the control-plane REST API. Each call attaches the
// bearer token, sends/parses JSON, and throws ApiError (carrying the status) so
// callers can distinguish documented failures (409 no-account vs 502 worker).
import { getToken } from './auth.js';

// ApiError preserves the HTTP status so the UI can branch on it (e.g. 409 → show
// the account-link form, 404/409 → guard-prompt edge cases).
export class ApiError extends Error {
  constructor(status, message) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

// Build an absolute same-origin URL. Relative paths work in the browser; this
// also resolves correctly under jsdom (origin http://localhost) in tests.
function url(path) {
  const origin =
    typeof location !== 'undefined' && location.origin && location.origin !== 'null'
      ? location.origin
      : 'http://localhost';
  return `${origin}/api${path}`;
}

async function request(method, path, body) {
  const headers = {};
  const token = getToken();
  if (token) headers['Authorization'] = `Bearer ${token}`;
  if (body !== undefined) headers['Content-Type'] = 'application/json';

  const res = await fetch(url(path), {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) throw await toError(res);
  return res;
}

async function toError(res) {
  let message = res.statusText;
  try {
    const body = await res.json();
    if (body && body.error) message = body.error;
  } catch {
    // non-JSON body; keep statusText
  }
  return new ApiError(res.status, message);
}

async function json(res) {
  return res.json();
}

// --- auth ---
export async function register(username, password) {
  return json(await request('POST', '/auth/register', { username, password }));
}
export async function login(username, password) {
  return json(await request('POST', '/auth/login', { username, password }));
}
export async function logout() {
  await request('POST', '/auth/logout');
}

// --- steam accounts ---
export async function listSteamAccounts() {
  return json(await request('GET', '/steam/accounts'));
}
// linkAccount starts a Steam link. With no arguments it starts a QR link (the
// challenge URL arrives over the WebSocket); with a username + password it starts
// the email-only / no-2FA credentials link.
export async function linkAccount(steamUsername = '', steamPassword = '') {
  return json(
    await request('POST', '/steam/accounts', {
      steam_username: steamUsername,
      steam_password: steamPassword,
    }),
  );
}
export async function deleteAccount(id) {
  await request('DELETE', `/steam/accounts/${id}`);
}
export async function submitAccountGuard(id, code) {
  await request('POST', `/steam/accounts/${id}/steamguard`, { code });
}

// --- friends ---
export async function getFriends() {
  return json(await request('GET', '/friends'));
}

// --- sessions ---
export async function startSession(targetSteamID) {
  return json(await request('POST', '/sessions', { target_steam_id: targetSteamID }));
}
export async function getSession(id) {
  return json(await request('GET', `/sessions/${id}`));
}
export async function stopSession(id) {
  await request('DELETE', `/sessions/${id}`);
}
export async function submitSessionGuard(id, code) {
  await request('POST', `/sessions/${id}/steamguard`, { code });
}
