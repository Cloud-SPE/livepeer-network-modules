package repo

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"github.com/ethereum/go-ethereum/common"
	bolt "go.etcd.io/bbolt"
)

const poolIdentityBucket = "regional_identity"
const regionalTermsBucket = "regional_terms"
const termsAcceptanceBucket = "regional_terms_acceptance"

var ErrTermsNotAccepted = errors.New("regional terms acceptance required")

func initRegional(tx *bolt.Tx) (string, error) {
	b := tx.Bucket([]byte(poolIdentityBucket))
	if b == nil {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return "", err
		}
		var err error
		b, err = tx.CreateBucket([]byte(poolIdentityBucket))
		if err != nil {
			return "", err
		}
		if err := b.Put([]byte("pool_id"), []byte("pool_"+hex.EncodeToString(id[:]))); err != nil {
			return "", err
		}
	}
	id := string(b.Get([]byte("pool_id")))
	if !strings.HasPrefix(id, "pool_") || len(id) != 37 {
		return "", errors.New("corrupt regional pool identity; restore the original database")
	}
	if _, err := hex.DecodeString(id[5:]); err != nil {
		return "", errors.New("corrupt regional pool identity encoding")
	}
	for _, name := range []string{regionalTermsBucket, termsAcceptanceBucket, "regional_window_holds", "terms_publications", deviceTransfersBucket} {
		if _, err := tx.CreateBucketIfNotExists([]byte(name)); err != nil {
			return "", err
		}
	}
	return id, nil
}

// PoolID is generated once in the same transaction as initial store migration.
// Display names and network addresses never participate in identity.
func (r *StateRepo) PoolID() string { return r.poolID }

func (r *StateRepo) PutRegionalTerms(term types.RegionalTerms) error {
	if term.PoolID != r.poolID {
		return errors.New("terms pool_id does not match this controller")
	}
	if term.Version == "" || strings.ContainsAny(term.Version, "\x00/\n") || len(term.Version) > 128 {
		return errors.New("invalid terms version")
	}
	if term.WindowRounds == 0 {
		term.WindowRounds = 14
	}
	if term.CommissionBPS > 10000 || term.ParticipationRules == "" || !term.ZeroWorkToOperator || !term.RoundingToOperator {
		return errors.New("terms require valid commission, participation rules and Model B disclosures")
	}
	return r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(regionalTermsBucket))
		if raw := b.Get([]byte(term.Version)); raw != nil {
			var old types.RegionalTerms
			if err := json.Unmarshal(raw, &old); err != nil {
				return err
			}
			term.CreatedAt = old.CreatedAt
			encoded, err := json.Marshal(term)
			if err != nil {
				return err
			}
			if !bytes.Equal(raw, encoded) {
				return errors.New("terms version is immutable")
			}
			return nil
		}
		if rounds := tx.Bucket([]byte("regional_round_numbers")); rounds != nil {
			if err := rounds.ForEach(func(key, _ []byte) error {
				number, err := strconv.ParseUint(string(key), 10, 64)
				if err != nil {
					return err
				}
				if number >= term.EffectiveRound {
					return errors.New("terms cannot rewrite reconciled rounds")
				}
				return nil
			}); err != nil {
				return err
			}
		}
		var latest *types.RegionalTerms
		if err := b.ForEach(func(_, v []byte) error {
			var item types.RegionalTerms
			if err := json.Unmarshal(v, &item); err != nil {
				return err
			}
			if latest == nil || item.EffectiveRound > latest.EffectiveRound {
				latest = &item
			}
			return nil
		}); err != nil {
			return err
		}
		if latest != nil && (term.EffectiveRound <= latest.EffectiveRound || (term.EffectiveRound-latest.EffectiveRound)%latest.WindowRounds != 0) {
			return errors.New("terms must append at an existing accounting-window boundary")
		}
		term.CreatedAt = time.Now().UTC()
		raw, err := json.Marshal(term)
		if err != nil {
			return err
		}
		return b.Put([]byte(term.Version), raw)
	})
}

func (r *StateRepo) ListRegionalTerms() ([]types.RegionalTerms, error) {
	return listJSON(r, regionalTermsBucket, func(a, b types.RegionalTerms) bool { return a.EffectiveRound < b.EffectiveRound })
}

func (r *StateRepo) RegionalTermsForRound(round uint64) (types.RegionalTerms, error) {
	terms, err := r.ListRegionalTerms()
	if err != nil {
		return types.RegionalTerms{}, err
	}
	var found *types.RegionalTerms
	for i := range terms {
		if terms[i].EffectiveRound <= round {
			found = &terms[i]
		}
	}
	if found == nil {
		return types.RegionalTerms{}, fmt.Errorf("no regional terms for round %d", round)
	}
	return *found, nil
}

func acceptanceKey(wallet, version string) string { return strings.ToLower(wallet) + "\x00" + version }

// AcceptRegionalTerms preserves the first acceptance time on retries. Callers
// supply an authenticated wallet, never a browser-provided acceptance timestamp.
func (r *StateRepo) AcceptRegionalTerms(poolID, wallet, version string) (types.TermsAcceptance, error) {
	return r.acceptRegionalTerms(poolID, wallet, version, false)
}
func (r *StateRepo) JoinRegionalTerms(poolID, wallet, version string) (types.TermsAcceptance, error) {
	return r.acceptRegionalTerms(poolID, wallet, version, true)
}
func (r *StateRepo) acceptRegionalTerms(poolID, wallet, version string, join bool) (types.TermsAcceptance, error) {
	var result types.TermsAcceptance
	if poolID != r.poolID || !common.IsHexAddress(wallet) {
		return result, errors.New("invalid pool or member wallet")
	}
	wallet = strings.ToLower(wallet)
	err := r.db.Update(func(tx *bolt.Tx) error {
		if tx.Bucket([]byte(regionalTermsBucket)).Get([]byte(version)) == nil {
			return errors.New("unknown regional terms version")
		}
		if join {
			var latest types.RegionalTerms
			if err := tx.Bucket([]byte(regionalTermsBucket)).ForEach(func(_, raw []byte) error {
				var term types.RegionalTerms
				if err := json.Unmarshal(raw, &term); err != nil {
					return err
				}
				if latest.Version == "" || term.EffectiveRound > latest.EffectiveRound {
					latest = term
				}
				return nil
			}); err != nil {
				return err
			}
			if latest.Version != version {
				return fmt.Errorf("join requires latest published regional terms")
			}
		}
		memberRaw := tx.Bucket([]byte(poolMembersBucket)).Get([]byte(wallet))
		var member types.PoolMember
		if memberRaw == nil && join {
			now := time.Now().UTC()
			member = types.PoolMember{PoolID: r.PoolID(), ID: wallet, EthAddress: wallet, PayoutMode: "eth", Status: types.MemberStatusActive, CreatedAt: now, UpdatedAt: now}
			var err error
			memberRaw, err = json.Marshal(member)
			if err != nil {
				return err
			}
			if err := tx.Bucket([]byte(poolMembersBucket)).Put([]byte(wallet), memberRaw); err != nil {
				return err
			}
		}
		if memberRaw == nil {
			return errors.New("wallet authentication required before joining")
		}
		if err := json.Unmarshal(memberRaw, &member); err != nil {
			return err
		}
		if member.Status != types.MemberStatusActive {
			return errors.New("member is not active")
		}
		b := tx.Bucket([]byte(termsAcceptanceBucket))
		key := []byte(acceptanceKey(wallet, version))
		if old := b.Get(key); old != nil {
			if err := json.Unmarshal(old, &result); err != nil {
				return err
			}
			return grantAcceptedTerms(tx, wallet, version)
		}
		result = types.TermsAcceptance{PoolID: r.poolID, MemberEthAddress: wallet, TermsVersion: version, AcceptedAt: time.Now().UTC()}
		raw, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if err := b.Put(key, raw); err != nil {
			return err
		}
		return grantAcceptedTerms(tx, wallet, version)
	})
	return result, err
}

func (r *StateRepo) RequireTermsAcceptance(wallet, version string) (types.TermsAcceptance, error) {
	var result types.TermsAcceptance
	err := getJSON(r, termsAcceptanceBucket, acceptanceKey(wallet, version), &result)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return result, fmt.Errorf("%w: %v", ErrTermsNotAccepted, err)
		}
		return result, err
	}
	return result, nil
}

// SaveHardwareOwnership commits the device grant and the broker credential's
// device-generation scope together. A crash cannot publish half a grant.
func (r *StateRepo) SaveHardwareOwnership(unit types.HardwareUnit) error {
	if unit.OwnershipGeneration == 0 {
		return errors.New("ownership generation required")
	}
	return r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(hostEnrollmentsBucket))
		raw := b.Get([]byte(unit.EnrollmentID))
		if raw == nil {
			return errors.New("ownership requires an enrollment")
		}
		var enrollment types.HostEnrollment
		if err := json.Unmarshal(raw, &enrollment); err != nil {
			return err
		}
		if !strings.EqualFold(enrollment.MemberEthAddress, unit.MemberEthAddress) || enrollment.Status == types.HostEnrollmentRetired || enrollment.Status == types.HostEnrollmentRevoked {
			return errors.New("ownership enrollment is not active for this wallet")
		}
		if enrollment.DeviceOwnership == nil {
			enrollment.DeviceOwnership = map[string]uint64{}
		}
		enrollment.DeviceOwnership[strings.ToLower(unit.GPUUUID)] = unit.OwnershipGeneration
		enrollment.PoolID = r.poolID
		encoded, err := json.Marshal(enrollment)
		if err != nil {
			return err
		}
		if err := b.Put([]byte(enrollment.ID), encoded); err != nil {
			return err
		}
		if unit.CreatedAt.IsZero() {
			unit.CreatedAt = time.Now().UTC()
		}
		unit.UpdatedAt = time.Now().UTC()
		if unit.State == "" {
			unit.State = types.HardwareUnitRegistered
		}
		encoded, err = json.Marshal(unit)
		if err != nil {
			return err
		}
		if err := guardDeviceTransferWrite(tx, hardwareUnitsBucket, encoded); err != nil {
			return err
		}
		return tx.Bucket([]byte(hardwareUnitsBucket)).Put([]byte(unit.ID), encoded)
	})
}

// grantAcceptedTerms refreshes only the latest published version, so accepting
// an archived version cannot roll an enrollment back to superseded terms.
func grantAcceptedTerms(tx *bolt.Tx, wallet, version string) error {
	var latest types.RegionalTerms
	if err := tx.Bucket([]byte(regionalTermsBucket)).ForEach(func(_, raw []byte) error {
		var term types.RegionalTerms
		if err := json.Unmarshal(raw, &term); err != nil {
			return err
		}
		if latest.Version == "" || term.EffectiveRound > latest.EffectiveRound {
			latest = term
		}
		return nil
	}); err != nil {
		return err
	}
	if latest.Version != version {
		return nil
	}
	b := tx.Bucket([]byte(hostEnrollmentsBucket))
	var updates []types.HostEnrollment
	if err := b.ForEach(func(_, raw []byte) error {
		var e types.HostEnrollment
		if err := json.Unmarshal(raw, &e); err != nil {
			return err
		}
		if strings.EqualFold(e.MemberEthAddress, wallet) && e.Status != types.HostEnrollmentRevoked && e.Status != types.HostEnrollmentRetired {
			e.TermsVersion = version
			updates = append(updates, e)
		}
		return nil
	}); err != nil {
		return err
	}
	for _, e := range updates {
		raw, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if err := b.Put([]byte(e.ID), raw); err != nil {
			return err
		}
	}
	return nil
}
