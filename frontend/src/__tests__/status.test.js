import { describe, it, expect } from 'vitest';
import { canSpectate, statusLabel } from '../status.js';
import { friendsList } from '../test/fixtures.js';

const [inMatch, online, offline] = friendsList;

describe('canSpectate', () => {
  it('is true only when the friend is in a match', () => {
    expect(canSpectate(inMatch)).toBe(true);
    expect(canSpectate(online)).toBe(false);
    expect(canSpectate(offline)).toBe(false);
    expect(canSpectate(null)).toBe(false);
    expect(canSpectate(undefined)).toBe(false);
  });
});

describe('statusLabel', () => {
  it('labels presence', () => {
    expect(statusLabel(inMatch)).toBe('in match');
    expect(statusLabel(online)).toBe('online');
    expect(statusLabel(offline)).toBe('offline');
    expect(statusLabel(null)).toBe('offline');
  });
});
