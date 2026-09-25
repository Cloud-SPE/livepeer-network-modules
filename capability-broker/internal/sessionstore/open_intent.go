package sessionstore

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
)

func (s *Store) sealReservation(r *OpenReservation) ([]byte, error) {
	clone := *r
	clone.AdmissionIntentSealed = nil
	if r.AdmissionIntent != nil {
		plain, err := json.Marshal(r.AdmissionIntent)
		if err != nil {
			return nil, err
		}
		nonce := make([]byte, s.aead.NonceSize())
		if _, err = rand.Read(nonce); err != nil {
			return nil, err
		}
		clone.AdmissionIntentSealed = append(nonce, s.aead.Seal(nil, nonce, plain, []byte(r.RequestID+":open-admission"))...)
	}
	return json.Marshal(clone)
}
func (s *Store) unsealReservation(raw []byte) (*OpenReservation, error) {
	var r OpenReservation
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	if len(r.AdmissionIntentSealed) > 0 {
		ns := s.aead.NonceSize()
		if len(r.AdmissionIntentSealed) < ns {
			return nil, fmt.Errorf("sealed open admission truncated")
		}
		plain, err := s.aead.Open(nil, r.AdmissionIntentSealed[:ns], r.AdmissionIntentSealed[ns:], []byte(r.RequestID+":open-admission"))
		if err != nil {
			return nil, fmt.Errorf("unseal open admission: %w", err)
		}
		if err = json.Unmarshal(plain, &r.AdmissionIntent); err != nil {
			return nil, err
		}
	}
	return &r, nil
}
