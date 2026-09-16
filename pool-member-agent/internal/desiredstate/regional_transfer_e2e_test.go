package desiredstate

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Invoked by the cross-component harness with a real Docker daemon. Hardware
// and sleeping workloads are synthetic; ComposeRunner must actually remove
// the stopped container before its production status report is submitted.
func TestRegionalTransferAgentApply(t *testing.T) {
	path := os.Getenv("REGIONAL_E2E_AGENT_APPLY")
	if path == "" {
		t.Skip("explicit Docker transfer acceptance fixture")
	}
	var c struct {
		URL, EnrollmentID, Token, ComposePath, ReportPath string
		SuppressReport                                    bool
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := New(c.URL, c.EnrollmentID, c.Token, 10*time.Second)
	doc, err := client.Fetch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	report := Apply(ctx, ComposeRunner{}, c.ComposePath, doc)
	for _, s := range report.Services {
		if s.Status == StatusFailed {
			t.Fatalf("actual Compose apply failed: %+v", s)
		}
	}
	raw, err = json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(c.ReportPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if !c.SuppressReport {
		if err = client.Report(ctx, report); err != nil {
			t.Fatal(err)
		}
	}
}
