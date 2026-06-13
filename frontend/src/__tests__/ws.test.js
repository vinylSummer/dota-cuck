import { describe, it, expect } from 'vitest';
import { routeEvent } from '../ws.js';
import { wsEvents } from '../test/fixtures.js';

describe('routeEvent', () => {
  it('routes session_state', () => {
    expect(routeEvent(wsEvents.sessionState)).toEqual({
      kind: 'session_state',
      scope: 'session',
      id: 's1',
      state: 'WATCHING',
    });
  });

  it('maps stream_ready to webrtcUrl', () => {
    expect(routeEvent(wsEvents.streamReady)).toEqual({
      kind: 'stream_ready',
      scope: 'session',
      id: 's1',
      webrtcUrl: 'https://dota.example.com/webrtc/live/match',
    });
  });

  it('maps account_linked to steamId', () => {
    expect(routeEvent(wsEvents.accountLinked)).toEqual({
      kind: 'account_linked',
      scope: 'account',
      id: 'a1',
      steamId: '76561198000000000',
    });
  });

  it('discriminates steam_guard by account_id vs session_id', () => {
    const acct = routeEvent(wsEvents.guardAccount);
    expect(acct).toMatchObject({ kind: 'steam_guard', scope: 'account', id: 'a1' });

    const sess = routeEvent(wsEvents.guardSession);
    expect(sess).toMatchObject({ kind: 'steam_guard', scope: 'session', id: 's1' });
  });

  it('discriminates error by account_id vs session_id', () => {
    expect(routeEvent(wsEvents.errorAccount)).toMatchObject({ scope: 'account', id: 'a1', code: 'LINK_FAILED' });
    expect(routeEvent(wsEvents.errorSession)).toMatchObject({ scope: 'session', id: 's1', code: 'DOTA_CRASH' });
  });

  it('ignores unknown or malformed events without throwing', () => {
    expect(routeEvent({ type: 'nope' })).toBeNull();
    expect(routeEvent({})).toBeNull();
    expect(routeEvent(null)).toBeNull();
    expect(routeEvent(undefined)).toBeNull();
  });
});
