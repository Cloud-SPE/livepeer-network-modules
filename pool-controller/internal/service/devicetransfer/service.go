// Package devicetransfer coordinates a durable source-side GPU handoff.
package devicetransfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/ownership"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
)

type Authority interface {
	Get(context.Context, string) (ownership.Record, error)
	Drain(context.Context, ownership.Request) (ownership.Record, error)
	Release(context.Context, ownership.Request) (ownership.Record, error)
}
type Broker interface {
	DrainDevice(context.Context, ownership.DeviceDrainRequest) (ownership.DeviceDrainProof, error)
}
type Service struct {
	Repo      *repo.StateRepo
	Authority Authority
	Broker    func(revenue.Source) (Broker, error)
}

func (s *Service) Begin(ctx context.Context, unit, wallet, destination, reason string) (repo.DeviceTransfer, error) {
	var item repo.DeviceTransfer
	err := s.Repo.WithDeviceTransferLock(func() error {
		if s.Authority == nil || s.Broker == nil {
			return fmt.Errorf("regional transfer dependencies unavailable")
		}
		sources, err := s.Repo.RevenueSources(nil)
		if err != nil {
			return err
		}
		var targets []revenue.Source
		for _, source := range sources {
			if source.State == "retired" {
				continue
			}
			if _, err := s.Broker(source.Source); err != nil {
				return err
			}
			targets = append(targets, source.Source)
		}
		item, err = s.Repo.BeginDeviceTransfer(unit, wallet, destination, reason, targets)
		if err != nil {
			return err
		}
		err = s.resume(ctx, item)
		refreshed, readErr := s.Repo.GetDeviceTransfer(item.ID)
		if readErr == nil {
			item = refreshed
		}
		if err != nil {
			return err
		}
		return readErr
	})
	return item, err
}
func (s *Service) Resume(ctx context.Context) error {
	return s.Repo.WithDeviceTransferLock(func() error {
		items, err := s.Repo.DeviceTransfers()
		if err != nil {
			return err
		}
		var failure error
		for _, item := range items {
			if item.Phase == "released" {
				continue
			}
			if err := s.resume(ctx, item); err != nil {
				failure = err
			}
		}
		return failure
	})
}
func (s *Service) resume(ctx context.Context, item repo.DeviceTransfer) error {
	if item.Phase == "released" {
		return nil
	}
	if s.Authority == nil || s.Broker == nil {
		return fmt.Errorf("regional transfer dependencies unavailable")
	}
	save := func() error {
		if err := s.Repo.SaveDeviceTransfer(item); err != nil {
			return err
		}
		var err error
		item, err = s.Repo.GetDeviceTransfer(item.ID)
		return err
	}
	hold := func(err error) error {
		item.LastError = err.Error()
		if writeErr := save(); writeErr != nil {
			return fmt.Errorf("%v; record hold: %w", err, writeErr)
		}
		return err
	}
	request := ownership.Request{PoolID: item.PoolID, DeviceID: item.DeviceID, EnrollmentID: item.EnrollmentID, MemberWallet: item.MemberWallet, ExpectedGeneration: item.Generation, DestinationPoolID: item.DestinationPoolID, Reason: item.Reason}
	if item.Phase == "requested" {
		record, err := s.Authority.Drain(ctx, request)
		if err != nil {
			return hold(err)
		}
		if record.PoolID != item.PoolID || record.EnrollmentID != item.EnrollmentID || record.DeviceID != item.DeviceID || record.Generation != item.Generation || record.State != "draining" || record.DestinationPoolID != item.DestinationPoolID {
			return hold(fmt.Errorf("ownership drain acknowledgment mismatch"))
		}
		item.Phase = "draining"
		item.LastError = ""
		if err := save(); err != nil {
			return err
		}
	}
	if item.Phase == "draining" || item.Phase == "revoking" {
		action := "drain"
		if item.Phase == "revoking" {
			action = "revoke"
		}
		var pending error
		for i, target := range item.Targets {
			broker, err := s.Broker(target.Source)
			if err != nil {
				pending = err
				continue
			}
			proof, err := broker.DrainDevice(ctx, ownership.DeviceDrainRequest{Action: action, PoolID: item.PoolID, EnrollmentID: item.EnrollmentID, DeviceID: item.DeviceID, Generation: item.Generation, Reason: item.Reason})
			if err != nil {
				pending = err
				continue
			}
			if !qualified(proof, item, target.Source) {
				pending = fmt.Errorf("broker %s device proof mismatch or stale", target.Source.BrokerID)
				continue
			}
			if action == "drain" {
				item.Targets[i].Drain = proof
			} else {
				item.Targets[i].Revocation = proof
			}
			if proof.PendingOperations+proof.ActiveAuthorizations+proof.UndeliveredReceipts+proof.UnqualifiedWork != 0 || action == "revoke" && !proof.Revoked {
				pending = fmt.Errorf("broker %s device work or revocation pending", target.Source.BrokerID)
			}
		}
		if pending != nil {
			return hold(pending)
		}
		if action == "drain" {
			item.Phase = "revoking"
		} else {
			item.Phase = "stopping"
		}
		item.LastError = ""
		if err := save(); err != nil {
			return err
		}
		if item.Phase == "revoking" {
			return s.resume(ctx, item)
		}
	}
	if item.Phase == "stopping" {
		if item.StopConfirmedAt.IsZero() {
			return hold(fmt.Errorf("waiting for revision-bound agent stop report"))
		}
		request.DrainEvidence = commitment(item.Targets)
		request.RevocationEvidence = request.DrainEvidence
		request.StopEvidence = commitment([]any{item.EnrollmentID, item.AssignmentIDs, item.StopRevision, item.StopConfirmedAt})
		record, err := s.Authority.Release(ctx, request)
		if err != nil {
			// A lost release reply may be followed by a destination claim before this
			// controller retries. Only a newer exclusive destination assignment proves
			// that the authority already completed this source's fenced release.
			current, readErr := s.Authority.Get(ctx, item.DeviceID)
			if readErr != nil || current.DeviceID != item.DeviceID || current.PoolID != item.DestinationPoolID || current.Generation <= item.Generation || current.MemberWallet != item.MemberWallet {
				return hold(err)
			}
		} else if record.DeviceID != item.DeviceID || record.EnrollmentID != item.EnrollmentID || record.PoolID != item.PoolID || record.Generation != item.Generation || record.State != "released" || record.DestinationPoolID != item.DestinationPoolID {
			return hold(fmt.Errorf("ownership release acknowledgment mismatch"))
		}
		item.Phase = "released"
		item.LastError = ""
		return save()
	}
	return nil
}
func qualified(p ownership.DeviceDrainProof, t repo.DeviceTransfer, s revenue.Source) bool {
	now := time.Now()
	return p.PoolID == t.PoolID && p.SourceID == s.SourceID && p.BrokerID == s.BrokerID && p.EnrollmentID == t.EnrollmentID && p.DeviceID == t.DeviceID && p.Generation == t.Generation && !p.StartedAt.IsZero() && !p.ObservedAt.IsZero() && now.Sub(p.ObservedAt) <= 2*time.Minute && !p.ObservedAt.After(now.Add(time.Minute))
}
func commitment(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
