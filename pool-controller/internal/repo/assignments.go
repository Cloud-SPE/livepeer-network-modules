package repo

import (
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func (r *StateRepo) PutAssignment(assignment types.Assignment) error {
	now := time.Now().UTC()
	if assignment.CreatedAt.IsZero() {
		assignment.CreatedAt = now
	}
	assignment.UpdatedAt = now
	if assignment.Status == "" {
		assignment.Status = types.AssignmentStatusActive
	}
	return putJSON(r, assignmentsBucket, assignment.ID, assignment)
}

func (r *StateRepo) GetAssignment(id string) (types.Assignment, error) {
	var out types.Assignment
	err := getJSON(r, assignmentsBucket, id, &out)
	return out, err
}

func (r *StateRepo) ListAssignments() ([]types.Assignment, error) {
	return listJSON(r, assignmentsBucket, func(left, right types.Assignment) bool {
		if left.OfferID != right.OfferID {
			return left.OfferID < right.OfferID
		}
		if left.MemberBackendID != right.MemberBackendID {
			return left.MemberBackendID < right.MemberBackendID
		}
		return left.ID < right.ID
	})
}

func (r *StateRepo) ListAssignmentsByOffer(offerID string) ([]types.Assignment, error) {
	items, err := r.ListAssignments()
	if err != nil {
		return nil, err
	}
	out := make([]types.Assignment, 0)
	for _, item := range items {
		if item.OfferID == offerID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *StateRepo) ListAssignmentsByBackend(memberBackendID string) ([]types.Assignment, error) {
	items, err := r.ListAssignments()
	if err != nil {
		return nil, err
	}
	out := make([]types.Assignment, 0)
	for _, item := range items {
		if item.MemberBackendID == memberBackendID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *StateRepo) SetAssignmentStatus(id string, status types.AssignmentStatus) error {
	item, err := r.GetAssignment(id)
	if err != nil {
		return err
	}
	item.Status = status
	item.UpdatedAt = time.Now().UTC()
	return putJSON(r, assignmentsBucket, item.ID, item)
}

func (r *StateRepo) DeleteAssignment(id string) error {
	return deleteKey(r, assignmentsBucket, id)
}
