import { useEffect, useState } from 'react';
import { Routes, Route, Navigate } from 'react-router-dom';
import { isAuthed, clearToken } from './auth.js';
import { logout as apiLogout } from './api.js';
import { connect, disconnect, subscribe } from './ws.js';
import Login from './pages/Login.jsx';
import Friends from './pages/Friends.jsx';
import Watch from './pages/Watch.jsx';
import SteamGuardModal from './components/SteamGuardModal.jsx';

// App owns auth state, the WebSocket lifecycle, and the global Steam Guard modal.
// A steam_guard event can arrive during either an account link or a session
// start, so the modal is rendered here, above the routed pages.
export default function App() {
  const [authed, setAuthed] = useState(isAuthed());
  const [guard, setGuard] = useState(null); // { scope, id, guardType }

  useEffect(() => {
    if (!authed) return undefined;
    connect();
    const unsub = subscribe((ev) => {
      if (ev.kind === 'steam_guard') {
        setGuard({ scope: ev.scope, id: ev.id, guardType: ev.guardType });
      }
    });
    return () => {
      unsub();
      disconnect();
    };
  }, [authed]);

  function onLogin() {
    setAuthed(true);
  }

  async function onLogout() {
    try {
      await apiLogout();
    } catch {
      // best-effort; clear locally regardless
    }
    clearToken();
    setGuard(null);
    setAuthed(false);
  }

  return (
    <>
      <Routes>
        <Route
          path="/login"
          element={authed ? <Navigate to="/" replace /> : <Login onLogin={onLogin} />}
        />
        <Route
          path="/"
          element={authed ? <Friends onLogout={onLogout} /> : <Navigate to="/login" replace />}
        />
        <Route
          path="/watch/:sessionId"
          element={authed ? <Watch /> : <Navigate to="/login" replace />}
        />
        <Route path="*" element={<Navigate to={authed ? '/' : '/login'} replace />} />
      </Routes>
      {guard && (
        <SteamGuardModal
          scope={guard.scope}
          id={guard.id}
          guardType={guard.guardType}
          onClose={() => setGuard(null)}
        />
      )}
    </>
  );
}
