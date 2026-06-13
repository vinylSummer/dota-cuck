import { useState } from 'react';
import { submitAccountGuard, submitSessionGuard } from '../api.js';

// SteamGuardModal collects a Steam Guard code and submits it to the account-link
// or session endpoint depending on the event scope that triggered it.
export default function SteamGuardModal({ scope, id, guardType, onClose }) {
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function submit(e) {
    e.preventDefault();
    setError('');
    setBusy(true);
    try {
      if (scope === 'account') await submitAccountGuard(id, code);
      else await submitSessionGuard(id, code);
      onClose();
    } catch (err) {
      setError(err.message || 'invalid code');
      setBusy(false);
    }
  }

  return (
    <div className="modal-backdrop">
      <div className="modal">
        <h2>Steam Guard</h2>
        <p>Enter the {guardType === 'EMAIL' ? 'code from your email' : 'code from your authenticator'}.</p>
        <form onSubmit={submit}>
          <input value={code} onChange={(e) => setCode(e.target.value)} autoFocus placeholder="code" />
          <button type="submit" disabled={busy}>
            Submit
          </button>
        </form>
        {error && <p className="error">{error}</p>}
      </div>
    </div>
  );
}
