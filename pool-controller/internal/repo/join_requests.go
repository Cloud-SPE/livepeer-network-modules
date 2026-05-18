package repo

import (
	"fmt"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func (r *StateRepo) PutJoinRequest(req types.JoinRequest) error {
	now := time.Now().UTC()
	if req.SubmittedAt.IsZero() {
		req.SubmittedAt = now
	}
	if req.Status == "" {
		req.Status = types.JoinRequestPending
	}
	return putJSON(r, joinRequestsBucket, req.ID, req)
}

func (r *StateRepo) GetJoinRequest(id string) (types.JoinRequest, error) {
	var out types.JoinRequest
	err := getJSON(r, joinRequestsBucket, id, &out)
	return out, err
}

func (r *StateRepo) ListJoinRequests() ([]types.JoinRequest, error) {
	return listJSON(r, joinRequestsBucket, func(left, right types.JoinRequest) bool {
		if !left.SubmittedAt.Equal(right.SubmittedAt) {
			return left.SubmittedAt.Before(right.SubmittedAt)
		}
		return left.ID < right.ID
	})
}

func (r *StateRepo) SetJoinRequestStatus(id string, status types.JoinRequestStatus, reviewReason string) error {
	item, err := r.GetJoinRequest(id)
	if err != nil {
		return err
	}
	item.Status = status
	item.ReviewReason = reviewReason
	now := time.Now().UTC()
	item.ReviewedAt = &now
	return putJSON(r, joinRequestsBucket, item.ID, item)
}

func (r *StateRepo) SetJoinRequestBackendVerificationResult(joinRequestID string, backendID string, status types.VerificationStatus, verificationErr string, verifiedAt time.Time) error {
	item, err := r.GetJoinRequest(joinRequestID)
	if err != nil {
		return err
	}
	for idx := range item.RequestedBackends {
		if item.RequestedBackends[idx].ID != backendID {
			continue
		}
		item.RequestedBackends[idx].VerificationStatus = status
		item.RequestedBackends[idx].VerificationError = verificationErr
		if !verifiedAt.IsZero() {
			ts := verifiedAt.UTC()
			item.RequestedBackends[idx].LastVerifiedAt = &ts
		}
		return putJSON(r, joinRequestsBucket, item.ID, item)
	}
	return fmt.Errorf("join request %q backend %q: not found", joinRequestID, backendID)
}
