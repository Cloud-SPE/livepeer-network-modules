package repo

import (
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func (r *StateRepo) PutMemberBackend(backend types.MemberBackend) error {
	now := time.Now().UTC()
	if backend.CreatedAt.IsZero() {
		backend.CreatedAt = now
	}
	backend.UpdatedAt = now
	if backend.Status == "" {
		backend.Status = types.BackendStatusActive
	}
	if backend.VerificationStatus == "" {
		backend.VerificationStatus = types.VerificationUnknown
	}
	return putJSON(r, memberBackendsBucket, backend.ID, backend)
}

func (r *StateRepo) GetMemberBackend(id string) (types.MemberBackend, error) {
	var out types.MemberBackend
	err := getJSON(r, memberBackendsBucket, id, &out)
	return out, err
}

func (r *StateRepo) ListMemberBackends() ([]types.MemberBackend, error) {
	return listJSON(r, memberBackendsBucket, func(left, right types.MemberBackend) bool {
		if left.MemberID != right.MemberID {
			return left.MemberID < right.MemberID
		}
		return left.ID < right.ID
	})
}

func (r *StateRepo) ListMemberBackendsByMember(memberID string) ([]types.MemberBackend, error) {
	items, err := r.ListMemberBackends()
	if err != nil {
		return nil, err
	}
	out := make([]types.MemberBackend, 0)
	for _, item := range items {
		if item.MemberID == memberID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *StateRepo) SetMemberBackendStatus(id string, status types.BackendStatus) error {
	item, err := r.GetMemberBackend(id)
	if err != nil {
		return err
	}
	item.Status = status
	item.UpdatedAt = time.Now().UTC()
	return putJSON(r, memberBackendsBucket, item.ID, item)
}

func (r *StateRepo) SetVerificationResult(id string, status types.VerificationStatus, verificationErr string, verifiedAt time.Time) error {
	item, err := r.GetMemberBackend(id)
	if err != nil {
		return err
	}
	item.VerificationStatus = status
	item.VerificationError = verificationErr
	if !verifiedAt.IsZero() {
		ts := verifiedAt.UTC()
		item.LastVerifiedAt = &ts
	}
	item.UpdatedAt = time.Now().UTC()
	return putJSON(r, memberBackendsBucket, item.ID, item)
}
