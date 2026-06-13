// Minimal WHEP client for mediamtx. Browser-only glue (RTCPeerConnection), not
// unit-tested. Negotiates a recvonly connection and attaches the remote stream
// to the given <video> element.
export async function startWhep(whepUrl, videoEl) {
  const pc = new RTCPeerConnection();
  pc.addTransceiver('video', { direction: 'recvonly' });
  pc.addTransceiver('audio', { direction: 'recvonly' });
  pc.ontrack = (e) => {
    videoEl.srcObject = e.streams[0];
  };

  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);

  const res = await fetch(whepUrl, {
    method: 'POST',
    headers: { 'Content-Type': 'application/sdp' },
    body: offer.sdp,
  });
  if (!res.ok) {
    pc.close();
    throw new Error(`WHEP negotiation failed: ${res.status}`);
  }
  const answer = await res.text();
  await pc.setRemoteDescription({ type: 'answer', sdp: answer });
  return pc;
}
