package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/pion/webrtc/v4"

	"rtspstreamer/internal/streaming"
)

type Server struct {
	logger  *log.Logger
	manager *streaming.CameraManager
}

func NewServer(logger *log.Logger, manager *streaming.CameraManager) *Server {
	return &Server{logger: logger, manager: manager}
}

func (s *Server) Router() http.Handler {
	r := mux.NewRouter()
	r.HandleFunc("/api/cameras", s.handleCameras).Methods(http.MethodGet)
	r.HandleFunc("/api/status", s.handleStatus).Methods(http.MethodGet)
	r.HandleFunc("/api/cameras/{ip}/test", s.handleTestCamera).Methods(http.MethodPost)
	r.HandleFunc("/api/stream/offer", s.handleOffer).Methods(http.MethodPost)
	r.HandleFunc("/api/stream/stop", s.handleStop).Methods(http.MethodPost)

	fs := http.FileServer(http.Dir("web"))
	r.PathPrefix("/").Handler(fs)
	return loggingMiddleware(r)
}

func (s *Server) handleCameras(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.manager.Cameras())
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.manager.Statuses())
}

func (s *Server) handleTestCamera(w http.ResponseWriter, r *http.Request) {
	ip := mux.Vars(r)["ip"]
	if err := s.manager.TestCamera(ip); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type offerRequest struct {
	IP   string `json:"ip"`
	SDP  string `json:"sdp"`
	Type string `json:"type"`
}

type offerResponse struct {
	ClientID string `json:"clientId"`
	IP       string `json:"ip"`
	Type     string `json:"type"`
	SDP      string `json:"sdp"`
}

func (s *Server) handleOffer(w http.ResponseWriter, r *http.Request) {
	var req offerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	sdpType, err := parseSDPType(req.Type)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	offer := webrtc.SessionDescription{Type: sdpType, SDP: req.SDP}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()

	clientID, answer, err := s.manager.CreateAnswer(ctx, req.IP, offer)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, offerResponse{ClientID: clientID, IP: req.IP, Type: answer.Type.String(), SDP: answer.SDP})
}

type stopRequest struct {
	IP       string `json:"ip"`
	ClientID string `json:"clientId"`
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	var req stopRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.manager.RemoveClient(req.IP, req.ClientID); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func parseSDPType(raw string) (webrtc.SDPType, error) {
	switch raw {
	case "offer":
		return webrtc.SDPTypeOffer, nil
	case "answer":
		return webrtc.SDPTypeAnswer, nil
	case "pranswer":
		return webrtc.SDPTypePranswer, nil
	case "rollback":
		return webrtc.SDPTypeRollback, nil
	default:
		return 0, fmt.Errorf("unsupported sdp type: %s", raw)
	}
}
