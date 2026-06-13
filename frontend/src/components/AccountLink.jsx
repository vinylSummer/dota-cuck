import { useEffect, useState } from 'react';
import { linkAccount } from '../api.js';
import { subscribe } from '../ws.js';

// AccountLink is shown when no Steam account is linked. It posts credentials to
// start the worker login; the outcome arrives over the WebSocket. A steam_guard
// event is handled by the global modal in App; here we react to the terminal
// account_linked / error events.
export default function AccountLink({ onLinked, onLogout }) {
  const [steamUsername, setSteamUsername] = useState('');
  const [steamPassword, setSteamPassword] = useState('');
  const [status, setStatus] = useState('idle'); // idle | linking | error
  const [error, setError] = useState('');

  useEffect(() => {
    const unsub = subscribe((ev) => {
      if (ev.scope !== 'account') return;
      if (ev.kind === 'account_linked') {
        setStatus('idle');
        onLinked();
      } else if (ev.kind === 'error') {
        setStatus('error');
        setError(ev.message || 'link failed');
      }
    });
    return unsub;
  }, [onLinked]);

  async function submit(e) {
    e.preventDefault();
    setError('');
    setStatus('linking');
    try {
      await linkAccount(steamUsername, steamPassword);
    } catch (err) {
      setStatus('error');
      setError(err.message || 'could not link account');
    }
  }

  return (
    <div className="card">
      <header className="row">
        <h1>Link your Steam account</h1>
        <button className="link" onClick={onLogout}>
          Log out
        </button>
      </header>
      <p>We need your Steam login to read your friends list and spectate matches.</p>
      <form onSubmit={submit}>
        <input
          placeholder="steam username"
          value={steamUsername}
          onChange={(e) => setSteamUsername(e.target.value)}
        />
        <input
          type="password"
          placeholder="steam password"
          value={steamPassword}
          onChange={(e) => setSteamPassword(e.target.value)}
        />
        <button type="submit" disabled={status === 'linking'}>
          {status === 'linking' ? 'Linking…' : 'Link account'}
        </button>
      </form>
      {status === 'linking' && <p>Logging in to Steam… you may be prompted for a Steam Guard code.</p>}
      {error && <p className="error">{error}</p>}
    </div>
  );
}
