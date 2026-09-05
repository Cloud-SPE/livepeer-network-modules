package certification

import (
	"fmt"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

const ExecutionPathBrokerVirtualBackend = "broker_virtual_backend"

// TemplateSource is the catalog this service certifies against. The
// catalog is files on disk, so it is handed in rather than read from
// the database.
type TemplateSource interface {
	Get(id string) (templates.Template, bool)
}

type Service struct {
	repo    *repo.StateRepo
	catalog TemplateSource
	now     func() time.Time
}

type CompleteRequest struct {
	RunID         string
	Passed        bool
	Results       []types.CertificationResult
	FailureReason string
}

func New(stateRepo *repo.StateRepo, catalog TemplateSource) *Service {
	return NewWithClock(stateRepo, catalog, nil)
}

func NewWithClock(stateRepo *repo.StateRepo, catalog TemplateSource, now func() time.Time) *Service {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{repo: stateRepo, catalog: catalog, now: now}
}

func (s *Service) StartAssignmentCertification(assignmentID string) (types.CertificationRun, error) {
	assignmentID = strings.TrimSpace(assignmentID)
	if assignmentID == "" {
		return types.CertificationRun{}, fmt.Errorf("assignment_id is required")
	}
	assignment, err := s.repo.GetTemplateAssignment(assignmentID)
	if err != nil {
		return types.CertificationRun{}, err
	}
	hardware, err := s.repo.GetHardwareUnit(assignment.HardwareUnitID)
	if err != nil {
		return types.CertificationRun{}, err
	}
	// Certify against a template the catalog actually defines: an
	// assignment naming a template this build does not ship has nothing
	// to prove, and starting a run for it would produce a verdict with
	// no recipe behind it.
	if s.catalog == nil {
		return types.CertificationRun{}, fmt.Errorf("template catalog is not loaded")
	}
	if _, ok := s.catalog.Get(assignment.TemplateID); !ok {
		return types.CertificationRun{}, fmt.Errorf("template %q is not in the catalog", assignment.TemplateID)
	}
	now := s.now()
	assignment.State = types.TemplateAssignmentTesting
	assignment.UpdatedAt = now
	hardware.State = types.HardwareUnitTesting
	hardware.UpdatedAt = now
	run := types.CertificationRun{
		ID:               fmt.Sprintf("cert-%s-%d", assignment.ID, now.UnixNano()),
		AssignmentID:     assignment.ID,
		HardwareUnitID:   assignment.HardwareUnitID,
		HostEnrollmentID: assignment.HostEnrollmentID,
		TemplateID:       assignment.TemplateID,
		ExecutionPath:    ExecutionPathBrokerVirtualBackend,
		Status:           types.CertificationRunning,
		StartedAt:        now,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.repo.PutTemplateAssignment(assignment); err != nil {
		return types.CertificationRun{}, err
	}
	if err := s.repo.PutHardwareUnit(hardware); err != nil {
		return types.CertificationRun{}, err
	}
	if err := s.repo.PutCertificationRun(run); err != nil {
		return types.CertificationRun{}, err
	}
	_ = s.repo.AppendAuditEvent(types.AuditEvent{
		Kind:         "certification_started",
		OccurredAt:   now,
		ResourceID:   run.ID,
		ResourceType: "certification_run",
		Details: map[string]any{
			"assignment_id":  run.AssignmentID,
			"execution_path": run.ExecutionPath,
		},
	})
	return run, nil
}

func (s *Service) CompleteRun(req CompleteRequest) (types.CertificationRun, error) {
	run, err := s.findRun(req.RunID)
	if err != nil {
		return types.CertificationRun{}, err
	}
	assignment, err := s.repo.GetTemplateAssignment(run.AssignmentID)
	if err != nil {
		return types.CertificationRun{}, err
	}
	hardware, err := s.repo.GetHardwareUnit(run.HardwareUnitID)
	if err != nil {
		return types.CertificationRun{}, err
	}
	now := s.now()
	run.Results = req.Results
	run.CompletedAt = now
	run.UpdatedAt = now
	if req.Passed {
		run.Status = types.CertificationPassed
		assignment.State = types.TemplateAssignmentProbationary
		assignment.ProbationStartedAt = now
		assignment.LastCertifiedAt = now
		hardware.State = types.HardwareUnitProbationary
	} else {
		run.Status = types.CertificationFailed
		run.FailureReason = strings.TrimSpace(req.FailureReason)
		assignment.State = types.TemplateAssignmentThrottled
		hardware.State = types.HardwareUnitThrottled
	}
	assignment.UpdatedAt = now
	hardware.UpdatedAt = now
	if err := s.repo.PutCertificationRun(run); err != nil {
		return types.CertificationRun{}, err
	}
	if err := s.repo.PutTemplateAssignment(assignment); err != nil {
		return types.CertificationRun{}, err
	}
	if err := s.repo.PutHardwareUnit(hardware); err != nil {
		return types.CertificationRun{}, err
	}
	_ = s.repo.AppendAuditEvent(types.AuditEvent{
		Kind:         "certification_completed",
		OccurredAt:   now,
		ResourceID:   run.ID,
		ResourceType: "certification_run",
		Details: map[string]any{
			"assignment_id": run.AssignmentID,
			"status":        run.Status,
		},
	})
	return run, nil
}

func (s *Service) findRun(runID string) (types.CertificationRun, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return types.CertificationRun{}, fmt.Errorf("run_id is required")
	}
	runs, err := s.repo.ListCertificationRuns()
	if err != nil {
		return types.CertificationRun{}, err
	}
	for _, run := range runs {
		if run.ID == runID {
			return run, nil
		}
	}
	return types.CertificationRun{}, fmt.Errorf("certification run %q not found", runID)
}
