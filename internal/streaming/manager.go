package streaming

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/google/uuid"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"

	"rtspstreamer/internal/config"
	"rtspstreamer/internal/onvifx"
)

const (
	idleStopTimeout = 30 * time.Second
	retryInterval   = 30 * time.Second
)

type State string

const (
	StateIdle         State = "idle"
	StateActive       State = "active"
	StateReconnecting State = "reconnecting"
	StateError        State = "error"
)

type CameraStatus struct {
	IP          string    `json:"ip"`
	State       State     `json:"state"`
	Clients     int       `json:"clients"`
	LastError   string    `json:"lastError,omitempty"`
	UpdatedAt   time.Time `json:"updatedAt"`
	LastRTSPURL string    `json:"lastRtspUrl,omitempty"`
}

type rtspResolver interface {
	DiscoverSubstreamRTSP(camera config.Camera) (string, error)
}

type CameraManager struct {
	logger   *log.Logger
	resolver rtspResolver

	cameras map[string]config.Camera

	mu       sync.RWMutex
	sessions map[string]*cameraSession
}

func NewManager(logger *log.Logger, cameras []config.Camera, resolver *onvifx.Service) *CameraManager {
	m := &CameraManager{
		logger:   logger,
		resolver: resolver,
		cameras:  make(map[string]config.Camera, len(cameras)),
		sessions: make(map[string]*cameraSession, len(cameras)),
	}
	for _, c := range cameras {
		m.cameras[c.IP] = c
		m.sessions[c.IP] = newCameraSession(c, logger, resolver)
	}
	return m
}

func (m *CameraManager) Cameras() []config.Camera {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]config.Camera, 0, len(m.cameras))
	for _, c := range m.cameras {
		out = append(out, c)
	}
	return out
}

func (m *CameraManager) Statuses() []CameraStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]CameraStatus, 0, len(m.sessions))
	for ip, s := range m.sessions {
		out = append(out, s.status(ip))
	}
	return out
}

func (m *CameraManager) EnsureCamera(ip string) (*cameraSession, error) {
	m.mu.RLock()
	s, ok := m.sessions[ip]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("camera %s not configured", ip)
	}
	return s, nil
}

func (m *CameraManager) CreateAnswer(ctx context.Context, ip string, offer webrtc.SessionDescription) (string, webrtc.SessionDescription, error) {
	session, err := m.EnsureCamera(ip)
	if err != nil {
		return "", webrtc.SessionDescription{}, err
	}
	return session.addClient(ctx, offer)
}

func (m *CameraManager) RemoveClient(ip, clientID string) error {
	session, err := m.EnsureCamera(ip)
	if err != nil {
		return err
	}
	session.removeClient(clientID)
	return nil
}

func (m *CameraManager) TestCamera(ip string) error {
	session, err := m.EnsureCamera(ip)
	if err != nil {
		return err
	}
	_, err = session.resolveRTSPURL()
	return err
}

type subscriber struct {
	pc    *webrtc.PeerConnection
	track *webrtc.TrackLocalStaticRTP
}

type cameraSession struct {
	camera   config.Camera
	logger   *log.Logger
	resolver rtspResolver

	mu          sync.RWMutex
	state       State
	lastError   string
	lastRTSPURL string
	updatedAt   time.Time
	clients     map[string]*subscriber
	stopTimer   *time.Timer
	cancelRun   context.CancelFunc
}

func newCameraSession(camera config.Camera, logger *log.Logger, resolver rtspResolver) *cameraSession {
	return &cameraSession{
		camera:    camera,
		logger:    logger,
		resolver:  resolver,
		state:     StateIdle,
		updatedAt: time.Now().UTC(),
		clients:   map[string]*subscriber{},
	}
}

func (s *cameraSession) status(ip string) CameraStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return CameraStatus{IP: ip, State: s.state, Clients: len(s.clients), LastError: s.lastError, UpdatedAt: s.updatedAt, LastRTSPURL: s.lastRTSPURL}
}

func (s *cameraSession) setState(state State, lastErr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
	s.lastError = lastErr
	s.updatedAt = time.Now().UTC()
}

func (s *cameraSession) resolveRTSPURL() (string, error) {
	u, err := s.resolver.DiscoverSubstreamRTSP(s.camera)
	if err != nil {
		s.setState(StateError, err.Error())
		return "", err
	}
	s.mu.Lock()
	s.lastRTSPURL = u
	s.updatedAt = time.Now().UTC()
	s.mu.Unlock()
	return u, nil
}

func (s *cameraSession) addClient(ctx context.Context, offer webrtc.SessionDescription) (string, webrtc.SessionDescription, error) {
	if err := s.ensureRunning(); err != nil {
		return "", webrtc.SessionDescription{}, err
	}

	mimeType := webrtc.MimeTypeH264
	track, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: mimeType, ClockRate: 90000}, "video", s.camera.IP)
	if err != nil {
		return "", webrtc.SessionDescription{}, err
	}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return "", webrtc.SessionDescription{}, err
	}

	if _, err := pc.AddTrack(track); err != nil {
		_ = pc.Close()
		return "", webrtc.SessionDescription{}, err
	}

	clientID := uuid.NewString()
	s.mu.Lock()
	if s.stopTimer != nil {
		s.stopTimer.Stop()
		s.stopTimer = nil
	}
	s.clients[clientID] = &subscriber{pc: pc, track: track}
	s.mu.Unlock()

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateDisconnected || state == webrtc.PeerConnectionStateClosed {
			s.removeClient(clientID)
		}
	})

	if err := pc.SetRemoteDescription(offer); err != nil {
		s.removeClient(clientID)
		return "", webrtc.SessionDescription{}, err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		s.removeClient(clientID)
		return "", webrtc.SessionDescription{}, err
	}
	gather := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		s.removeClient(clientID)
		return "", webrtc.SessionDescription{}, err
	}
	select {
	case <-gather:
	case <-ctx.Done():
		s.removeClient(clientID)
		return "", webrtc.SessionDescription{}, ctx.Err()
	}

	s.logger.Printf("[client] connected camera=%s client=%s total=%d", s.camera.IP, clientID, s.clientCount())
	return clientID, *pc.LocalDescription(), nil
}

func (s *cameraSession) removeClient(clientID string) {
	s.mu.Lock()
	sub, ok := s.clients[clientID]
	if ok {
		delete(s.clients, clientID)
	}
	remaining := len(s.clients)
	if remaining == 0 && s.stopTimer == nil {
		s.stopTimer = time.AfterFunc(idleStopTimeout, func() {
			s.stopIfIdle()
		})
	}
	s.mu.Unlock()

	if ok {
		_ = sub.pc.Close()
		s.logger.Printf("[client] disconnected camera=%s client=%s remaining=%d", s.camera.IP, clientID, remaining)
	}
}

func (s *cameraSession) clientCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.clients)
}

func (s *cameraSession) ensureRunning() error {
	s.mu.Lock()
	if s.cancelRun != nil {
		s.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelRun = cancel
	s.mu.Unlock()

	go s.runRTSP(ctx)
	return nil
}

func (s *cameraSession) stopIfIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopTimer = nil
	if len(s.clients) > 0 {
		return
	}
	if s.cancelRun != nil {
		s.cancelRun()
		s.cancelRun = nil
		s.state = StateIdle
		s.updatedAt = time.Now().UTC()
		s.logger.Printf("[stream] stopped camera=%s reason=idle-timeout", s.camera.IP)
	}
}

func (s *cameraSession) runRTSP(ctx context.Context) {
	s.logger.Printf("[stream] starting camera=%s", s.camera.IP)
	defer func() {
		s.mu.Lock()
		s.cancelRun = nil
		s.mu.Unlock()
	}()

	for {
		if ctx.Err() != nil {
			return
		}
		rtspURL, err := s.resolveRTSPURL()
		if err != nil {
			s.logger.Printf("[onvif] discovery failed camera=%s error=%v retry=%s", s.camera.IP, err, retryInterval)
			s.setState(StateReconnecting, err.Error())
			if !sleepContext(ctx, retryInterval) {
				return
			}
			continue
		}

		if err := s.consumeRTSP(ctx, rtspURL); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			s.logger.Printf("[stream] camera=%s error=%v retry=%s", s.camera.IP, err, retryInterval)
			s.setState(StateReconnecting, err.Error())
			if !sleepContext(ctx, retryInterval) {
				return
			}
			continue
		}
	}
}

func (s *cameraSession) consumeRTSP(ctx context.Context, rtspURL string) error {
	c := &gortsplib.Client{}
	defer c.Close()

	u, err := description.ParseURL(rtspURL)
	if err != nil {
		return err
	}

	desc, _, err := c.Describe(u)
	if err != nil {
		return fmt.Errorf("describe: %w", err)
	}

	var medi *description.Media
	var forma format.Format
	for _, media := range desc.Medias {
		for _, f := range media.Formats {
			if _, ok := f.(*format.H264); ok {
				medi = media
				forma = f
				break
			}
		}
		if medi != nil {
			break
		}
	}
	if medi == nil {
		return fmt.Errorf("no h264 media found in substream")
	}

	if _, err = c.Setup(desc.BaseURL, medi, 0, 0); err != nil {
		return fmt.Errorf("setup: %w", err)
	}

	pkt := &rtp.Packet{}
	c.OnPacketRTP(medi, forma, func(packet *rtp.Packet) {
		*pkt = *packet
		s.broadcastRTP(pkt)
	})

	if _, err = c.Play(nil); err != nil {
		return fmt.Errorf("play: %w", err)
	}

	s.setState(StateActive, "")
	s.logger.Printf("[stream] active camera=%s rtsp=%s", s.camera.IP, rtspURL)

	done := make(chan error, 1)
	go func() {
		done <- c.Wait()
	}()

	select {
	case <-ctx.Done():
		return context.Canceled
	case err := <-done:
		return err
	}
}

func (s *cameraSession) broadcastRTP(pkt *rtp.Packet) {
	s.mu.RLock()
	subs := make([]*subscriber, 0, len(s.clients))
	for _, sub := range s.clients {
		subs = append(subs, sub)
	}
	s.mu.RUnlock()

	for _, sub := range subs {
		copyPkt := *pkt
		if err := sub.track.WriteRTP(&copyPkt); err != nil {
			// Best-effort relay; connection callback removes dead peers.
		}
	}
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
