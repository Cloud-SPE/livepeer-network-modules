package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/registry"
)

type runtimeStatusResponse struct {
	LoadedRevision       string    `json:"loaded_revision,omitempty"`
	LoadedConfigPath     string    `json:"loaded_config_path,omitempty"`
	LoadedAt             time.Time `json:"loaded_at,omitempty"`
	LastReloadStartedAt  time.Time `json:"last_reload_started_at,omitempty"`
	LastReloadFinishedAt time.Time `json:"last_reload_finished_at,omitempty"`
	LastReloadStatus     string    `json:"last_reload_status,omitempty"`
	LastReloadError      string    `json:"last_reload_error,omitempty"`
}

func (s *Server) handleOfferings(w http.ResponseWriter, r *http.Request) {
	cfg := s.currentConfig()
	if cfg == nil {
		http.Error(w, "runtime config is not loaded", http.StatusInternalServerError)
		return
	}
	payload := registry.BuildOfferings(cfg, s.currentMetadata())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) handleRegistryHealth(w http.ResponseWriter, r *http.Request) {
	healthMgr := s.currentHealth()
	if healthMgr == nil {
		http.Error(w, "health manager is not available", http.StatusInternalServerError)
		return
	}
	registry.WriteHealthResponse(w, healthMgr, s.currentMetadata(), s.currentPoolSnapshot())
}

func (s *Server) handleRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	status := s.runtimeStatus()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(status)
}

func (s *Server) handleRuntimeReload(w http.ResponseWriter, r *http.Request) {
	status, err := s.reloadRuntime()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(status)
}

func (s *Server) runtimeStatus() runtimeStatusResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return runtimeStatusResponse{
		LoadedRevision:       s.loadedRevision,
		LoadedConfigPath:     s.loadedConfigPath,
		LoadedAt:             s.loadedAt,
		LastReloadStartedAt:  s.lastReloadStartedAt,
		LastReloadFinishedAt: s.lastReloadFinishedAt,
		LastReloadStatus:     s.lastReloadStatus,
		LastReloadError:      s.lastReloadError,
	}
}

func (s *Server) reloadRuntime() (runtimeStatusResponse, error) {
	if s.configPath == "" {
		return s.runtimeStatus(), fmt.Errorf("runtime reload requires a configured config path")
	}
	startedAt := time.Now().UTC()
	s.mu.Lock()
	s.lastReloadStartedAt = startedAt
	s.lastReloadStatus = "started"
	s.lastReloadError = ""
	s.mu.Unlock()

	cfg, err := config.Load(s.configPath)
	if err != nil {
		s.finishReload(startedAt, "failed", err.Error(), "", nil, nil)
		return s.runtimeStatus(), err
	}
	loadedRevision, loadedConfigPath, err := loadRuntimeRevision(s.configPath, cfg)
	if err != nil {
		s.finishReload(startedAt, "failed", err.Error(), "", nil, nil)
		return s.runtimeStatus(), err
	}
	metadata := newMetadataCatalog()
	refreshMetadataCatalog(context.Background(), &http.Client{Timeout: 2 * time.Second}, cfg, metadata)
	healthMgr := health.New(cfg)
	if err := validateConfigAgainstRegistries(cfg, s.modes, s.extractors); err != nil {
		s.finishReload(startedAt, "failed", err.Error(), "", nil, nil)
		return s.runtimeStatus(), err
	}
	s.finishReload(startedAt, "applied", "", loadedRevision, cfg, healthMgr)
	s.mu.Lock()
	s.loadedConfigPath = loadedConfigPath
	s.metadata = metadata
	s.mu.Unlock()
	return s.runtimeStatus(), nil
}

func (s *Server) finishReload(startedAt time.Time, status, reloadError, revision string, cfg *config.Config, healthMgr *health.Manager) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastReloadStartedAt = startedAt
	s.lastReloadFinishedAt = time.Now().UTC()
	s.lastReloadStatus = status
	s.lastReloadError = reloadError
	if status == "applied" {
		s.cfg = cfg
		s.health = healthMgr
		s.loadedRevision = revision
		s.loadedAt = s.lastReloadFinishedAt
	}
}
