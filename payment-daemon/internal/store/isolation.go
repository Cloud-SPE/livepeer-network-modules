package store

import (
	"crypto/rand"
	"encoding/hex"
	bolt "go.etcd.io/bbolt"
)

// TicketStreamID is generated once per sender database, never per process or mint.
func (s *Store) TicketStreamID() (string, error) {
	var id string
	err := s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte("sender_identity"))
		if err != nil {
			return err
		}
		if v := b.Get([]byte("ticket_stream_id")); v != nil {
			id = string(v)
			return nil
		}
		var raw [32]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return err
		}
		id = hex.EncodeToString(raw[:])
		return b.Put([]byte("ticket_stream_id"), []byte(id))
	})
	return id, err
}
