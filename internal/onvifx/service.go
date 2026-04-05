package onvifx

import (
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/use-go/onvif"
	"github.com/use-go/onvif/media"
	xsdonvif "github.com/use-go/onvif/xsd/onvif"

	"rtspstreamer/internal/config"
)

type Service struct {
	logger *log.Logger
}

func New(logger *log.Logger) *Service {
	return &Service{logger: logger}
}

func (s *Service) DiscoverSubstreamRTSP(camera config.Camera) (string, error) {
	s.logger.Printf("[onvif] connecting camera=%s", camera.IP)
	dev, err := onvif.NewDevice(onvif.DeviceParams{
		Xaddr:    fmt.Sprintf("http://%s/onvif/device_service", camera.IP),
		Username: camera.Username,
		Password: camera.Password,
	})
	if err != nil {
		return "", fmt.Errorf("create onvif device: %w", err)
	}

	profilesRaw, err := dev.CallMethod(media.GetProfiles{})
	if err != nil {
		return "", fmt.Errorf("get profiles: %w", err)
	}

	profiles := media.GetProfilesResponse{}
	if err := onvif.ParseSOAPResponse(profilesRaw, &profiles); err != nil {
		return "", fmt.Errorf("parse profiles: %w", err)
	}

	if len(profiles.Profiles) < 2 {
		return "", fmt.Errorf("camera %s has %d profile(s), need at least 2 for substream", camera.IP, len(profiles.Profiles))
	}

	subProfile := profiles.Profiles[1]
	s.logger.Printf("[onvif] discovered profiles=%d, using index=1 token=%s camera=%s", len(profiles.Profiles), subProfile.Token, camera.IP)

	streamRaw, err := dev.CallMethod(media.GetStreamUri{
		StreamSetup: xsdonvif.StreamSetup{
			Stream: xsdonvif.StreamType("RTP-Unicast"),
			Transport: xsdonvif.Transport{
				Protocol: xsdonvif.TransportProtocol("RTSP"),
			},
		},
		ProfileToken: subProfile.Token,
	})
	if err != nil {
		return "", fmt.Errorf("get stream uri: %w", err)
	}

	streamResp := media.GetStreamUriResponse{}
	if err := onvif.ParseSOAPResponse(streamRaw, &streamResp); err != nil {
		return "", fmt.Errorf("parse stream uri response: %w", err)
	}

	rtspURL := streamResp.MediaUri.Uri
	if rtspURL == "" {
		return "", fmt.Errorf("empty rtsp uri")
	}

	withCreds, err := injectRTSPCredentials(rtspURL, camera.Username, camera.Password)
	if err != nil {
		return "", err
	}

	s.logger.Printf("[onvif] extracted substream rtsp camera=%s uri=%s", camera.IP, sanitizeURL(withCreds))
	return withCreds, nil
}

func injectRTSPCredentials(rawURL, username, password string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse rtsp url: %w", err)
	}
	u.User = url.UserPassword(username, password)
	return u.String(), nil
}

func sanitizeURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "invalid-url"
	}
	if u.User != nil {
		u.User = url.UserPassword("***", "***")
	}
	return strings.ReplaceAll(u.String(), "%2A", "*")
}
