import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { login, register } from '../api.js';
import { setToken } from '../auth.js';

// Login doubles as register (same fields); both end by storing the JWT and
// landing on the friends page.
export default function Login({ onLogin }) {
  const [mode, setMode] = useState('login');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const navigate = useNavigate();

  async function submit(e) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      const fn = mode === 'login' ? login : register;
      const { token } = await fn(username, password);
      setToken(token);
      onLogin();
      navigate('/');
    } catch (err) {
      setError(err.message || 'request failed');
      setBusy(false);
    }
  }

  return (
    <div className="card">
      <h1>Dota Spectator</h1>
      <form onSubmit={submit}>
        <input
          placeholder="username"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          autoFocus
        />
        <input
          type="password"
          placeholder="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        <button type="submit" disabled={busy}>
          {mode === 'login' ? 'Log in' : 'Register'}
        </button>
      </form>
      {error && <p className="error">{error}</p>}
      <button
        className="link"
        onClick={() => {
          setError('');
          setMode(mode === 'login' ? 'register' : 'login');
        }}
      >
        {mode === 'login' ? 'Need an account? Register' : 'Have an account? Log in'}
      </button>
    </div>
  );
}
