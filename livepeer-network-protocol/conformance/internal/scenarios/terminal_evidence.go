package scenarios

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/conformance/internal/harness"
)

func terminalEvidenceScenario() harness.Scenario {
	return harness.Scenario{Name: "paid-session/terminal-evidence-stable-positive-sequence", Spec: "paid-session §3.5", Run: func(c *harness.Ctx) error {
		if c.SettlementSigner == "" || c.RestartBroker == nil {
			return fmt.Errorf("%w: signed broker restart required", harness.ErrSkip)
		}
		open, _, err := openHappySession(c, "terminal-evidence")
		if err != nil {
			return err
		}
		id, credential := harness.FieldString(open.JSON(), "session_id"), harness.FieldString(open.JSON(), "credential")
		closed, err := c.SessionEnd(id, credential, "gateway_close")
		if err != nil || closed.Status != 200 {
			return fmt.Errorf("end: %v %d", err, closed.Status)
		}
		first := closed.Header.Get(harness.HdrSettlement)
		signer, err := harness.RecoverSettlementSigner(first)
		if err != nil || !strings.EqualFold(signer, c.SettlementSigner) {
			return fmt.Errorf("terminal signature mismatch: %v", err)
		}
		raw, err := base64.StdEncoding.DecodeString(first)
		if err != nil {
			return err
		}
		var env struct {
			Payload struct {
				Sequence string `json:"settlement_seq"`
				State    string `json:"state"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			return err
		}
		seq, err := strconv.ParseUint(env.Payload.Sequence, 10, 64)
		if err != nil || seq == 0 || env.Payload.State != "closed" {
			return fmt.Errorf("invalid terminal sequence/state: %+v", env.Payload)
		}
		for i := 0; i < 2; i++ {
			query, err := c.QuerySettlement(id)
			if err != nil || query.Status != 200 || query.Header.Get(harness.HdrSettlement) != first {
				return fmt.Errorf("lookup changed terminal evidence: %v", err)
			}
			again, err := c.SessionEnd(id, credential, "different_reason")
			if err != nil || again.Status != 200 || again.Header.Get(harness.HdrSettlement) != first {
				return fmt.Errorf("repeat close changed terminal evidence: %v", err)
			}
			if i == 0 {
				if err := c.RestartBroker(); err != nil {
					return err
				}
			}
		}
		return nil
	}}
}
