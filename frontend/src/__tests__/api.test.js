import { describe, it, expect, beforeAll, afterAll, afterEach } from 'vitest';
import { http, HttpResponse } from 'msw';
import { setupServer } from 'msw/node';
import * as api from '../api.js';
import { setToken, clearToken } from '../auth.js';
import { friendsList, loginResponse, sessionResponse } from '../test/fixtures.js';

const server = setupServer();

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }));
afterEach(() => {
  server.resetHandlers();
  clearToken();
});
afterAll(() => server.close());

describe('api request contract', () => {
  it('login posts credentials and returns the token', async () => {
    let body;
    server.use(
      http.post('/api/auth/login', async ({ request }) => {
        body = await request.json();
        return HttpResponse.json(loginResponse);
      }),
    );
    const out = await api.login('alice', 'hunter2');
    expect(body).toEqual({ username: 'alice', password: 'hunter2' });
    expect(out).toEqual(loginResponse);
  });

  it('attaches the bearer token on authenticated calls', async () => {
    setToken('jwt.test.token');
    let authHeader;
    server.use(
      http.get('/api/friends', ({ request }) => {
        authHeader = request.headers.get('authorization');
        return HttpResponse.json(friendsList);
      }),
    );
    const out = await api.getFriends();
    expect(authHeader).toBe('Bearer jwt.test.token');
    expect(out).toEqual(friendsList);
  });

  it('maps linkAccount field names to the API shape', async () => {
    let body;
    server.use(
      http.post('/api/steam/accounts', async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ id: 'a1', steam_username: 'alice_dota' }, { status: 201 });
      }),
    );
    await api.linkAccount('alice_dota', 's3cr3t');
    expect(body).toEqual({ steam_username: 'alice_dota', steam_password: 's3cr3t' });
  });

  it('maps startSession to target_steam_id', async () => {
    let body;
    server.use(
      http.post('/api/sessions', async ({ request }) => {
        body = await request.json();
        return HttpResponse.json(sessionResponse, { status: 201 });
      }),
    );
    const s = await api.startSession('76561198000000001');
    expect(body).toEqual({ target_steam_id: '76561198000000001' });
    expect(s.id).toBe('sess-1');
  });

  it('surfaces 409 (no account) and 502 (worker) from getFriends distinctly', async () => {
    server.use(
      http.get('/api/friends', () =>
        HttpResponse.json({ error: 'no steam account linked' }, { status: 409 }),
      ),
    );
    await expect(api.getFriends()).rejects.toMatchObject({ status: 409 });

    server.use(
      http.get('/api/friends', () =>
        HttpResponse.json({ error: 'could not fetch friends' }, { status: 502 }),
      ),
    );
    await expect(api.getFriends()).rejects.toMatchObject({ status: 502 });
  });

  it('surfaces 404 and 409 from the account guard submit', async () => {
    server.use(
      http.post('/api/steam/accounts/:id/steamguard', () =>
        HttpResponse.json({ error: 'steam account not found' }, { status: 404 }),
      ),
    );
    await expect(api.submitAccountGuard('a1', '123')).rejects.toMatchObject({ status: 404 });

    server.use(
      http.post('/api/steam/accounts/:id/steamguard', () =>
        HttpResponse.json({ error: 'no steam guard prompt in progress' }, { status: 409 }),
      ),
    );
    await expect(api.submitAccountGuard('a1', '123')).rejects.toMatchObject({ status: 409 });
  });

  it('uses the error body message in ApiError', async () => {
    server.use(
      http.post('/api/auth/login', () =>
        HttpResponse.json({ error: 'invalid credentials' }, { status: 401 }),
      ),
    );
    await expect(api.login('a', 'b')).rejects.toThrow('invalid credentials');
  });
});
