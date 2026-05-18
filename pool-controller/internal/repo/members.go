package repo

import (
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func (r *StateRepo) PutMember(member types.MemberRecord) error {
	now := time.Now().UTC()
	if member.CreatedAt.IsZero() {
		member.CreatedAt = now
	}
	member.UpdatedAt = now
	if member.Status == "" {
		member.Status = types.MemberStatusActive
	}
	return putJSON(r, membersBucket, member.ID, member)
}

func (r *StateRepo) GetMember(id string) (types.MemberRecord, error) {
	var out types.MemberRecord
	err := getJSON(r, membersBucket, id, &out)
	return out, err
}

func (r *StateRepo) ListMembers() ([]types.MemberRecord, error) {
	return listJSON(r, membersBucket, func(left, right types.MemberRecord) bool {
		if left.EthAddress != right.EthAddress {
			return left.EthAddress < right.EthAddress
		}
		return left.ID < right.ID
	})
}

func (r *StateRepo) SetMemberStatus(id string, status types.MemberStatus) error {
	item, err := r.GetMember(id)
	if err != nil {
		return err
	}
	item.Status = status
	item.UpdatedAt = time.Now().UTC()
	return putJSON(r, membersBucket, item.ID, item)
}
