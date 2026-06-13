// Token storage and the auth predicate. The JWT lives in localStorage; the
// route guard and api layer are pure functions of its presence.

const TOKEN_KEY = 'dota_token';

export function setToken(token) {
  localStorage.setItem(TOKEN_KEY, token);
}

export function getToken() {
  return localStorage.getItem(TOKEN_KEY);
}

export function clearToken() {
  localStorage.removeItem(TOKEN_KEY);
}

// isAuthed is the route-guard decision: authenticated iff a token is stored.
export function isAuthed() {
  return !!getToken();
}
