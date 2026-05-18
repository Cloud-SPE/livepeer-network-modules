package backendverify

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

type Service struct {
	Repo   *repo.StateRepo
	Client *http.Client
}

type Result struct {
	BackendID          string                   `json:"backend_id"`
	VerificationStatus types.VerificationStatus `json:"verification_status"`
	VerificationError  string                   `json:"verification_error,omitempty"`
	VerifiedAt         time.Time                `json:"verified_at"`
}

func New(repo *repo.StateRepo) *Service {
	return &Service{
		Repo: repo,
		Client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (s *Service) VerifyJoinRequest(joinRequestID string) ([]Result, error) {
	req, err := s.Repo.GetJoinRequest(joinRequestID)
	if err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(req.RequestedBackends))
	for _, backend := range req.RequestedBackends {
		result := s.verifyRequestedBackend(backend)
		if err := s.Repo.SetJoinRequestBackendVerificationResult(joinRequestID, backend.ID, result.VerificationStatus, result.VerificationError, result.VerifiedAt); err != nil {
			return nil, err
		}
		out = append(out, result)
	}
	return out, nil
}

func (s *Service) VerifyMemberBackend(backendID string) (Result, error) {
	backend, err := s.Repo.GetMemberBackend(backendID)
	if err != nil {
		return Result{}, err
	}
	result := s.verifyMemberBackend(backend)
	if err := s.Repo.SetVerificationResult(backendID, result.VerificationStatus, result.VerificationError, result.VerifiedAt); err != nil {
		return Result{}, err
	}
	return result, nil
}

func (s *Service) verifyRequestedBackend(backend types.RequestedBackend) Result {
	status, verifyErr, verifiedAt := s.runProbe(backend.Transport, backend.URL, backend.Auth, backend.HealthProbe)
	return Result{
		BackendID:          backend.ID,
		VerificationStatus: status,
		VerificationError:  verifyErr,
		VerifiedAt:         verifiedAt,
	}
}

func (s *Service) verifyMemberBackend(backend types.MemberBackend) Result {
	status, verifyErr, verifiedAt := s.runProbe(backend.Transport, backend.URL, backend.Auth, backend.HealthProbe)
	return Result{
		BackendID:          backend.ID,
		VerificationStatus: status,
		VerificationError:  verifyErr,
		VerifiedAt:         verifiedAt,
	}
}

func (s *Service) runProbe(transport string, backendURL string, auth config.AuthConfig, probe config.HealthProbe) (types.VerificationStatus, string, time.Time) {
	verifiedAt := time.Now().UTC()
	if strings.TrimSpace(transport) == "" {
		return types.VerificationFailing, "transport is required", verifiedAt
	}
	targetURL, err := probeURL(backendURL, probe)
	if err != nil {
		return types.VerificationFailing, err.Error(), verifiedAt
	}
	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		return types.VerificationFailing, err.Error(), verifiedAt
	}
	if err := applyAuth(req, auth); err != nil {
		return types.VerificationFailing, err.Error(), verifiedAt
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	timeout := probeTimeout(probe)
	if timeout > 0 {
		client = &http.Client{Timeout: timeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return types.VerificationFailing, err.Error(), verifiedAt
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return types.VerificationFailing, fmt.Sprintf("probe returned HTTP %d", resp.StatusCode), verifiedAt
	}
	return types.VerificationPassing, "", verifiedAt
}

func probeURL(backendURL string, probe config.HealthProbe) (string, error) {
	if raw, ok := probe.Config["url"].(string); ok && strings.TrimSpace(raw) != "" {
		return strings.TrimSpace(raw), nil
	}
	if strings.TrimSpace(backendURL) != "" {
		return strings.TrimSpace(backendURL), nil
	}
	return "", fmt.Errorf("probe url is required")
}

func probeTimeout(probe config.HealthProbe) time.Duration {
	if probe.TimeoutMS > 0 {
		return time.Duration(probe.TimeoutMS) * time.Millisecond
	}
	return 5 * time.Second
}

func applyAuth(req *http.Request, auth config.AuthConfig) error {
	switch strings.TrimSpace(auth.Method) {
	case "", "none":
		return nil
	case "bearer":
		secret := strings.TrimSpace(auth.SecretRef)
		if secret == "" {
			return fmt.Errorf("auth.secret_ref is required for bearer auth")
		}
		if strings.HasPrefix(secret, "env://") {
			value := strings.TrimSpace(os.Getenv(strings.TrimPrefix(secret, "env://")))
			if value == "" {
				return fmt.Errorf("auth secret env var %q is empty", strings.TrimPrefix(secret, "env://"))
			}
			secret = value
		}
		req.Header.Set("Authorization", "Bearer "+secret)
		return nil
	default:
		return fmt.Errorf("unsupported auth method %q", auth.Method)
	}
}
