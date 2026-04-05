# RTSP Streamer (ONVIF + WebRTC DVR-style Live View)

Lightweight Go service for live camera monitoring with:

- ONVIF profile discovery and RTSP URL extraction (uses **second profile/substream only**).
- On-demand RTSP session lifecycle.
- Single shared stream per camera reused across multiple clients.
- RTSP -> WebRTC relay-first delivery (no transcoding path).
- Daily rolling logs in `logs/YYYY-MM-DD.log`.
- Built-in DVR-style browser monitor (`web/`) and external integration sample (`integration/player.html`).

## Run

```bash
go mod tidy
go run ./cmd/server -cameras camerainfo.json -listen :8080
```

Open:

- Main UI: `http://localhost:8080/`
- Integration sample: `http://localhost:8080/../integration/player.html` (open file directly or host separately)

## API

- `GET /api/cameras` -> configured cameras
- `GET /api/status` -> per camera state (`active|idle|reconnecting|error`) + client count
- `POST /api/cameras/{ip}/test` -> ONVIF + RTSP discovery test
- `POST /api/stream/offer` -> WebRTC offer/answer signaling
- `POST /api/stream/stop` -> detach client from shared camera stream

### Offer request

```json
{
  "ip": "192.168.1.100",
  "type": "offer",
  "sdp": "v=0..."
}
```

### Offer response

```json
{
  "clientId": "uuid",
  "ip": "192.168.1.100",
  "type": "answer",
  "sdp": "v=0..."
}
```

## Notes

- Reconnect loop retries every 30 seconds when camera/RTSP fails.
- When client count reaches zero, stream stops after 30 second idle timeout.
- Designed around substream-first low resource usage for multi-camera grid viewing.
