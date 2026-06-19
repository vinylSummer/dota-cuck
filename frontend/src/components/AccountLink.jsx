import { useEffect, useState } from 'react';
import { QRCodeSVG } from 'qrcode.react';
import { linkAccount } from '../api.js';
import { subscribe } from '../ws.js';

// AccountLink is shown when no Steam account is linked. The primary path is a QR
// link: "Link with Steam" posts with no credentials, then the worker pushes a
// steam_qr event whose challenge URL we render as a QR for the Steam mobile app
// to scan. Accounts that can't scan a QR (email-only / no-2FA) use the
// "Sign in with a password instead" fallback, which posts credentials; a
// steam_guard event for the emailed code is handled by the global modal in App.
// Either way the terminal account_linked / error event resolves the flow.
//
// A POST happens only once, after the user picks a mode (QR creates the account
// row, so we can't switch modes afterward — V1 allows one account per user).
export default function AccountLink({ onLinked, onLogout }) {
  const [mode, setMode] = useState('choose'); // choose | qr | credentials
  const [steamUsername, setSteamUsername] = useState('');
  const [steamPassword, setSteamPassword] = useState('');
  const [challengeUrl, setChallengeUrl] = useState('');
  const [status, setStatus] = useState('idle'); // idle | linking | error
  const [error, setError] = useState('');

  useEffect(() => {
    const unsub = subscribe((ev) => {
      if (ev.scope !== 'account') return;
      if (ev.kind === 'steam_qr') {
        setChallengeUrl(ev.challengeUrl);
      } else if (ev.kind === 'account_linked') {
        setStatus('idle');
        onLinked();
      } else if (ev.kind === 'error') {
        setStatus('error');
        setError(ev.message || 'link failed');
      }
    });
    return unsub;
  }, [onLinked]);

  async function startQr() {
    setError('');
    setStatus('linking');
    setMode('qr');
    try {
      await linkAccount(); // no credentials => QR mode
    } catch (err) {
      setStatus('error');
      setError(err.message || 'could not start QR sign-in');
    }
  }

  async function submitCredentials(e) {
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

      {mode === 'choose' && (
        <div className="link-choose">
          <button type="submit" onClick={startQr}>
            Link with Steam (scan a QR code)
          </button>
          <button className="link" onClick={() => setMode('credentials')}>
            Sign in with a password instead
          </button>
        </div>
      )}

      {mode === 'qr' && (
        <div className="link-qr">
          {challengeUrl ? (
            <>
              <p>Open the Steam Mobile app and scan this code:</p>
              <QRCodeSVG value={challengeUrl} size={192} />
            </>
          ) : (
            <p>Starting QR sign-in…</p>
          )}
        </div>
      )}

      {mode === 'credentials' && (
        <form onSubmit={submitCredentials}>
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
          {status === 'linking' && (
            <p>Signing in to Steam… you may be prompted for an emailed Steam Guard code.</p>
          )}
        </form>
      )}

      {error && <p className="error">{error}</p>}
    </div>
  );
}
