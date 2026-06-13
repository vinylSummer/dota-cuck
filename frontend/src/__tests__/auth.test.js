import { describe, it, expect, afterEach } from 'vitest';
import { setToken, getToken, clearToken, isAuthed } from '../auth.js';

afterEach(() => clearToken());

describe('auth token store', () => {
  it('is unauthenticated with no token', () => {
    expect(isAuthed()).toBe(false);
    expect(getToken()).toBeNull();
  });

  it('is authenticated after setToken', () => {
    setToken('jwt.test.token');
    expect(isAuthed()).toBe(true);
    expect(getToken()).toBe('jwt.test.token');
  });

  it('clears on logout', () => {
    setToken('jwt.test.token');
    clearToken();
    expect(isAuthed()).toBe(false);
  });
});
