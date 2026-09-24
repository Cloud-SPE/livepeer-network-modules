package manifestcache

import (
	"bytes"
	"encoding/gob"
	"errors"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

var discoveryBucket = []byte("manifest_discovery")

func (r *repo) GetDiscovery(addr types.EthAddress) (types.DiscoveryStatus, bool, error) {
	var st types.DiscoveryStatus
	raw, err := r.s.Get(discoveryBucket, key(addr))
	if errors.Is(err, store.ErrNotFound) {
		return st, false, nil
	}
	if err != nil {
		return st, false, err
	}
	err = gob.NewDecoder(bytes.NewReader(raw)).Decode(&st)
	return st, err == nil, err
}
func (r *repo) PutDiscovery(addr types.EthAddress, st types.DiscoveryStatus) error {
	var b bytes.Buffer
	if err := gob.NewEncoder(&b).Encode(st); err != nil {
		return err
	}
	return r.s.Put(discoveryBucket, key(addr), b.Bytes())
}
func (m *meteredRepo) GetDiscovery(addr types.EthAddress) (types.DiscoveryStatus, bool, error) {
	return m.inner.GetDiscovery(addr)
}
func (m *meteredRepo) PutDiscovery(addr types.EthAddress, st types.DiscoveryStatus) error {
	return m.inner.PutDiscovery(addr, st)
}

func (r *repo) ListDiscovery() ([]types.EthAddress, error) {
	var out []types.EthAddress
	err := r.s.ForEach(discoveryBucket, func(k, _ []byte) error { out = append(out, types.EthAddress(string(k))); return nil })
	return out, err
}
func (m *meteredRepo) ListDiscovery() ([]types.EthAddress, error) { return m.inner.ListDiscovery() }
