package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

// The fixture catalog. One template, deliberately minimal.
//
// The repo's real catalog is exercised separately (see
// TestRealCatalogPlacesOnRealHardware). This one is fixed so that a
// pricing or wording change in a shipped template cannot make the seam
// test go red for a reason that has nothing to do with the seams.
const fixtureTemplate = `id: e2e-chat
capability: openai:chat-completions
offering_id: e2e-chat
protocol: paid-job/v1
display_name: E2E chat
description: Fixture workload for the end-to-end pool exercise.
price_default: { amount_wei: "1000000000", per_units: 1000 }
capacity: { max_in_flight: 2, queue_limit: 4 }
extra:
  provider: e2e
constraints:
  tier: standard
match:
  identity.openai.model: e2e-model
certification:
  - { name: smoke, type: request, required: true, config: { transport: unary, body: { model: "{{identity.openai.model}}", messages: [ { role: user, content: ping } ] }, assert: [ "$.choices[0].message.content" ] } }
requirements:
  gpu_classes: [rtx-4090]
priority: 10
stacking: { primary: true, secondary_on: [] }
runner_compose:
  image: example.invalid/e2e-chat:1
  env:
    E2E_FIXTURE: "1"
probation: { share_ppm: 20000, max_in_flight: 1, min_jobs: 20 }
active: { share_cap_ppm: 150000 }
commission_bps: 1000
`

// The session fixture. A paid-session workload reports usage on a
// callback rather than in a response body, which is the half of
// certification the job template above cannot exercise at all.
const fixtureSessionTemplate = `id: e2e-stream
capability: livepeer:meet/sfu-room
offering_id: e2e-stream
protocol: paid-session/v1
display_name: E2E session
description: Fixture session workload for the end-to-end pool exercise.
price_default: { amount_wei: "1000000000", per_units: 1 }
capacity: { max_in_flight: 2, queue_limit: 4 }
match:
  identity.provider: e2e
session_policy:
  attachment: inband-ws
  refill: extensible
  lease_policy: funding-tracking
  lease_max_seconds: 3600
  burn_rate_per_second: 1.0
  min_runway_units: 60
  heartbeat: { interval_seconds: 5, missed_threshold: 3 }
certification:
  - name: open
    type: request
    required: true
    config:
      hold_ms: 200
  # The step this fixture exists for: the runner must prove it can be
  # billed before the pool sells its work.
  - { name: usage, type: usage, required: true, config: { min_units: 1, window_ms: 5000 } }
requirements:
  gpu_classes: [rtx-4090]
priority: 20
stacking: { primary: true, secondary_on: [] }
runner_compose:
  image: example.invalid/e2e-stream:1
probation: { share_ppm: 20000, max_in_flight: 1, min_jobs: 20 }
active: { share_cap_ppm: 150000 }
commission_bps: 1000
`

// fixtureCatalog writes the fixture to its own directory.
func fixtureCatalog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"e2e-chat.yaml":   fixtureTemplate,
		"e2e-stream.yaml": fixtureSessionTemplate,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// repoCatalog is the catalog this build actually ships.
func repoCatalog(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../templates")
	if err != nil {
		t.Fatalf("resolve catalog: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("shipped catalog missing at %s: %v", dir, err)
	}
	return dir
}
