const state = {
  cameras: [],
  sessions: new Map(), // ip -> {pc, video, clientId}
  layout: 20,
};

const tableBody = document.querySelector('#camera-table tbody');
const canvas = document.getElementById('gridCanvas');
const ctx = canvas.getContext('2d');

async function fetchJSON(url, opts) {
  const res = await fetch(url, opts);
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

function computeGrid(count) {
  const cols = Math.ceil(Math.sqrt(count));
  const rows = Math.ceil(count / cols);
  return { cols, rows };
}

function draw() {
  const streams = Array.from(state.sessions.entries());
  ctx.fillStyle = '#000';
  ctx.fillRect(0, 0, canvas.width, canvas.height);

  const maxTiles = Math.min(state.layout, streams.length || state.layout);
  const { cols, rows } = computeGrid(maxTiles || 1);
  const tileW = Math.floor(canvas.width / cols);
  const tileH = Math.floor(canvas.height / rows);

  for (let i = 0; i < Math.min(streams.length, state.layout); i++) {
    const [ip, session] = streams[i];
    const x = (i % cols) * tileW;
    const y = Math.floor(i / cols) * tileH;

    if (session.video.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA) {
      ctx.drawImage(session.video, x, y, tileW, tileH);
    }

    ctx.strokeStyle = '#1fbdff';
    ctx.strokeRect(x + 0.5, y + 0.5, tileW - 1, tileH - 1);
    ctx.fillStyle = '#1fbdff';
    ctx.font = '14px sans-serif';
    ctx.fillText(ip, x + 8, y + 20);
  }

  requestAnimationFrame(draw);
}

function sdpType(type) {
  return type === 'offer' ? 'offer' : 'answer';
}

async function startCamera(ip) {
  if (state.sessions.has(ip)) return;

  const pc = new RTCPeerConnection();
  pc.addTransceiver('video', { direction: 'recvonly' });

  const video = document.createElement('video');
  video.autoplay = true;
  video.muted = true;
  video.playsInline = true;
  video.style.width = '1px';
  video.style.height = '1px';
  document.getElementById('hidden-videos').appendChild(video);

  pc.ontrack = (ev) => {
    video.srcObject = ev.streams[0];
  };

  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);

  const answer = await fetchJSON('/api/stream/offer', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ ip, sdp: offer.sdp, type: sdpType(offer.type) }),
  });

  await pc.setRemoteDescription({ type: answer.type, sdp: answer.sdp });
  state.sessions.set(ip, { pc, video, clientId: answer.clientId });
  await refresh();
}

async function stopCamera(ip) {
  const session = state.sessions.get(ip);
  if (!session) return;
  await fetchJSON('/api/stream/stop', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ ip, clientId: session.clientId }),
  }).catch(() => {});
  session.pc.close();
  session.video.remove();
  state.sessions.delete(ip);
  await refresh();
}

async function testCamera(ip) {
  try {
    await fetchJSON(`/api/cameras/${encodeURIComponent(ip)}/test`, { method: 'POST' });
    alert(`Camera ${ip} ONVIF test succeeded`);
  } catch (err) {
    alert(`Camera ${ip} test failed: ${err.message}`);
  }
}

function renderTable(statuses) {
  const byIP = new Map(statuses.map((s) => [s.ip, s]));
  tableBody.innerHTML = '';

  for (const cam of state.cameras) {
    const st = byIP.get(cam.ip) || { state: 'idle', clients: 0 };
    const tr = document.createElement('tr');
    tr.innerHTML = `
      <td>${cam.ip}</td>
      <td>${st.state}</td>
      <td>${st.clients}</td>
      <td>
        <button data-action="start" data-ip="${cam.ip}">Start</button>
        <button data-action="stop" data-ip="${cam.ip}">Stop</button>
        <button data-action="test" data-ip="${cam.ip}">Test</button>
      </td>`;
    tableBody.appendChild(tr);
  }

  for (const btn of tableBody.querySelectorAll('button')) {
    const ip = btn.dataset.ip;
    if (btn.dataset.action === 'start') btn.onclick = () => startCamera(ip);
    if (btn.dataset.action === 'stop') btn.onclick = () => stopCamera(ip);
    if (btn.dataset.action === 'test') btn.onclick = () => testCamera(ip);
  }
}

async function refresh() {
  const [cameras, statuses] = await Promise.all([
    fetchJSON('/api/cameras'),
    fetchJSON('/api/status'),
  ]);
  state.cameras = cameras;
  renderTable(statuses);
}

document.getElementById('refresh').onclick = () => refresh();
document.getElementById('layout').onchange = (e) => { state.layout = Number(e.target.value); };

refresh();
requestAnimationFrame(draw);
