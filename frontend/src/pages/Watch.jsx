import { useEffect, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { getSession, stopSession } from '../api.js';
import { subscribe } from '../ws.js';
import { startWhep } from '../webrtc.js';

// Watch shows the fullscreen stream for one session. It waits for a webrtc_url
// (from the initial status fetch or a stream_ready push), then negotiates WHEP.
export default function Watch() {
  const { sessionId } = useParams();
  const [webrtcUrl, setWebrtcUrl] = useState(null);
  const [state, setState] = useState('STARTING');
  const [error, setError] = useState('');
  const videoRef = useRef(null);
  const pcRef = useRef(null);
  const navigate = useNavigate();

  // Listen for session events and pick up an already-ready stream on mount.
  useEffect(() => {
    getSession(sessionId)
      .then((s) => {
        setState(s.state);
        if (s.webrtc_url) setWebrtcUrl(s.webrtc_url);
      })
      .catch(() => {
        // session may not exist yet on the control plane; rely on push events
      });

    const unsub = subscribe((ev) => {
      if (ev.id !== sessionId) return;
      if (ev.kind === 'stream_ready') {
        setState('WATCHING');
        setWebrtcUrl(ev.webrtcUrl);
      } else if (ev.kind === 'session_state') {
        setState(ev.state);
      } else if (ev.kind === 'error') {
        setError(ev.message || ev.code);
      }
    });
    return unsub;
  }, [sessionId]);

  // Negotiate WHEP once we have a URL and the <video> is mounted.
  useEffect(() => {
    if (!webrtcUrl || !videoRef.current) return undefined;
    let cancelled = false;
    startWhep(webrtcUrl, videoRef.current)
      .then((pc) => {
        if (cancelled) pc.close();
        else pcRef.current = pc;
      })
      .catch((err) => setError(err.message));
    return () => {
      cancelled = true;
      if (pcRef.current) {
        pcRef.current.close();
        pcRef.current = null;
      }
    };
  }, [webrtcUrl]);

  async function disconnect() {
    try {
      await stopSession(sessionId);
    } catch {
      // ignore; navigate away regardless
    }
    navigate('/');
  }

  return (
    <div className="watch">
      <video ref={videoRef} autoPlay playsInline controls />
      <div className="overlay">
        {!webrtcUrl && !error && <p>Starting stream… ({state})</p>}
        {error && <p className="error">{error}</p>}
        <button onClick={disconnect}>Disconnect</button>
      </div>
    </div>
  );
}
