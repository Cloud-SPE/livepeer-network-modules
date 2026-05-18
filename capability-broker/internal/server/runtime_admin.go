package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/registry"
)

type runtimeStatusResponse struct {
	LoadedRevision       string    `json:"loaded_revision,omitempty"`
	LoadedConfigPath     string    `json:"loaded_config_path,omitempty"`
	LoadedAt             time.Time `json:"loaded_at,omitempty"`
	LastReloadAttemptID  string    `json:"last_reload_attempt_id,omitempty"`
	LastReloadStartedAt  time.Time `json:"last_reload_started_at,omitempty"`
	LastReloadFinishedAt time.Time `json:"last_reload_finished_at,omitempty"`
	LastReloadStatus     string    `json:"last_reload_status,omitempty"`
	LastReloadError      string    `json:"last_reload_error,omitempty"`
	History              []runtimeHistoryEntry `json:"history,omitempty"`
}

type runtimeHistoryEntry struct {
	AttemptID      string    `json:"attempt_id,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	FinishedAt     time.Time `json:"finished_at,omitempty"`
	Status         string    `json:"status,omitempty"`
	Error          string    `json:"error,omitempty"`
	LoadedRevision string    `json:"loaded_revision,omitempty"`
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
	if !s.requireAdminAuth(w, r) {
		return
	}
	status := s.runtimeStatus()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(status)
}

func (s *Server) handleRuntimeReload(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminAuth(w, r) {
		return
	}
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
		LastReloadAttemptID:  s.lastReloadAttemptID,
		LastReloadStartedAt:  s.lastReloadStartedAt,
		LastReloadFinishedAt: s.lastReloadFinishedAt,
		LastReloadStatus:     s.lastReloadStatus,
		LastReloadError:      s.lastReloadError,
		History:              append([]runtimeHistoryEntry(nil), s.reloadHistory...),
	}
}

func (s *Server) requireAdminAuth(w http.ResponseWriter, r *http.Request) bool {
	s.mu.RLock()
	token := s.adminToken
	s.mu.RUnlock()
	if strings.TrimSpace(token) == "" {
		http.NotFound(w, r)
		return false
	}
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if authz != "Bearer "+token {
		w.Header().Set("WWW-Authenticate", `Bearer realm="capability-broker-admin"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func (s *Server) reloadRuntime() (runtimeStatusResponse, error) {
	if s.configPath == "" {
		return s.runtimeStatus(), fmt.Errorf("runtime reload requires a configured config path")
	}
	startedAt := time.Now().UTC()
	attemptID := fmt.Sprintf("reload-%d", startedAt.UnixNano())
	s.mu.Lock()
	s.lastReloadAttemptID = attemptID
	s.lastReloadStartedAt = startedAt
	s.lastReloadStatus = "started"
	s.lastReloadError = ""
	s.mu.Unlock()

	cfg, err := config.Load(s.configPath)
	if err != nil {
		s.finishReload(attemptID, startedAt, "failed", err.Error(), "", nil, nil)
		return s.runtimeStatus(), err
	}
	loadedRevision, loadedConfigPath, err := loadRuntimeRevision(s.configPath, cfg)
	if err != nil {
		s.finishReload(attemptID, startedAt, "failed", err.Error(), "", nil, nil)
		return s.runtimeStatus(), err
	}
	metadata := newMetadataCatalog()
	refreshMetadataCatalog(context.Background(), &http.Client{Timeout: 2 * time.Second}, cfg, metadata)
	if err := validateConfigAgainstRegistries(cfg, s.modes, s.extractors); err != nil {
		s.finishReload(attemptID, startedAt, "failed", err.Error(), "", nil, nil)
		return s.runtimeStatus(), err
	}
	var previousSnapshots []health.Snapshot
	if previous := s.currentHealth(); previous != nil {
		previousSnapshots = previous.Snapshot().Capabilities
	}
	healthMgr := health.NewWithSnapshots(cfg, previousSnapshots)
	s.finishReload(attemptID, startedAt, "applied", "", loadedRevision, cfg, healthMgr)
	s.mu.Lock()
	s.loadedConfigPath = loadedConfigPath
	s.metadata = metadata
	s.mu.Unlock()
	s.startHealthLoop(healthMgr)
	return s.runtimeStatus(), nil
}

func (s *Server) finishReload(attemptID string, startedAt time.Time, status, reloadError, revision string, cfg *config.Config, healthMgr *health.Manager) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastReloadAttemptID = attemptID
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
	s.recordReloadHistory(runtimeHistoryEntry{
		AttemptID:      attemptID,
		StartedAt:      startedAt,
		FinishedAt:     s.lastReloadFinishedAt,
		Status:         status,
		Error:          reloadError,
		LoadedRevision: revision,
	})
}

func (s *Server) recordReloadHistory(entry runtimeHistoryEntry) {
	if entry.Status == "" {
		return
	}
	s.reloadHistory = append(s.reloadHistory, entry)
	if len(s.reloadHistory) > 10 {
		s.reloadHistory = append([]runtimeHistoryEntry(nil), s.reloadHistory[len(s.reloadHistory)-10:]...)
	}
}
