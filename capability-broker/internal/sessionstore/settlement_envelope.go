package sessionstore

import (
	"bytes"
	"fmt"
)

func (s *Store) RecordTerminalSettlementEnvelope(id string, payload []byte, envelope string) (string, error) {
	var result string
	err := s.Update(id, func(r *Record) error {
		if !r.Terminal() || len(payload) == 0 || !bytes.Equal(r.TerminalSettlement, payload) {
			return fmt.Errorf("terminal settlement changed or missing")
		}
		if r.TerminalSettlementEnvelope == "" {
			r.TerminalSettlementEnvelope = envelope
		}
		result = r.TerminalSettlementEnvelope
		return nil
	})
	return result, err
}
