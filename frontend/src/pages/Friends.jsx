import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { getFriends, startSession } from '../api.js';
import { canSpectate, statusLabel } from '../status.js';
import AccountLink from '../components/AccountLink.jsx';

// Friends is the home page: the friend list with live status and a Spectate
// button (enabled only for friends currently in a match). A 409 from the API
// means no Steam account is linked yet, so we show the link form instead.
export default function Friends({ onLogout }) {
  const [friends, setFriends] = useState([]);
  const [needsAccount, setNeedsAccount] = useState(false);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const navigate = useNavigate();

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const list = await getFriends();
      setFriends(list);
      setNeedsAccount(false);
    } catch (err) {
      if (err.status === 409) {
        setNeedsAccount(true);
      } else {
        setError(err.message || 'could not load friends');
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  async function spectate(steamId) {
    setError('');
    try {
      const session = await startSession(steamId);
      navigate(`/watch/${session.id}`);
    } catch (err) {
      setError(err.message || 'could not start session');
    }
  }

  if (needsAccount) {
    return <AccountLink onLinked={load} onLogout={onLogout} />;
  }

  return (
    <div className="card">
      <header className="row">
        <h1>Friends</h1>
        <span>
          <button onClick={load} disabled={loading}>
            Refresh
          </button>
          <button className="link" onClick={onLogout}>
            Log out
          </button>
        </span>
      </header>

      {error && <p className="error">{error}</p>}
      {loading && <p>Loading…</p>}

      {!loading && friends.length === 0 && <p>No friends found.</p>}

      <ul className="friends">
        {friends.map((f) => (
          <li key={f.steam_id} className="row">
            <span>
              {f.persona_name} <em className={`status ${statusLabel(f).replace(' ', '-')}`}>{statusLabel(f)}</em>
            </span>
            <button onClick={() => spectate(f.steam_id)} disabled={!canSpectate(f)}>
              Spectate
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
