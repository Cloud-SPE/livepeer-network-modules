package desiredstate

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func secretDocument() Document {
	return Document{EnrollmentID: "host-1", Revision: "r1", Services: []Service{{Name: "live", SecretEnv: []string{"MASTER_KEY", "BROKER_TOKEN"}, LocalBearerEnv: "BROKER_TOKEN", ComposeFragment: "  live:\n    image: runner:1\n    environment:\n      MASTER_KEY: \"{{secret.MASTER_KEY}}\"\n      BROKER_TOKEN: \"{{secret.BROKER_TOKEN}}\"\n"}}}
}
func TestRunnerSecretsRestartIsolationAndPrivateCompose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runners.yaml")
	doc := secretDocument()
	first, err := ResolveRuntime(path, doc)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ResolveRuntime(path, doc)
	if err != nil {
		t.Fatal(err)
	}
	if first.Services[0].ComposeFragment != second.Services[0].ComposeFragment || first.Services[0].RuntimeBearer != second.Services[0].RuntimeBearer || strings.Contains(first.Services[0].ComposeFragment, "{{secret.") {
		t.Fatal("unstable or unresolved secrets")
	}
	decoded, err := base64.StdEncoding.DecodeString(first.Services[0].RuntimeBearer)
	if err != nil || len(decoded) != 32 {
		t.Fatal("wrong secret strength")
	}
	other := doc
	other.EnrollmentID = "host-2"
	separate, err := ResolveRuntime(path, other)
	if err != nil {
		t.Fatal(err)
	}
	if separate.Services[0].RuntimeBearer == first.Services[0].RuntimeBearer {
		t.Fatal("cross-enrollment secret reuse")
	}
	other = doc
	other.Services = append([]Service(nil), doc.Services...)
	other.Services[0].Name = "another"
	separate, err = ResolveRuntime(path, other)
	if err != nil {
		t.Fatal(err)
	}
	if separate.Services[0].RuntimeBearer == first.Services[0].RuntimeBearer {
		t.Fatal("cross-assignment secret reuse")
	}
	if err = (ComposeRunner{}).WriteCompose(path, RenderCompose(first)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("resolved compose must be private", err)
	}
	raw, _ := json.Marshal(StatusReport{Revision: doc.Revision, Services: []ServiceStatus{{Name: "live", Status: StatusRunning}}})
	if strings.Contains(string(raw), first.Services[0].RuntimeBearer) {
		t.Fatal("secret in report")
	}
	doc.Services[0].Stop = true
	stopped, err := ResolveRuntime(path, doc)
	if err != nil || strings.Contains(RenderCompose(stopped), "MASTER_KEY") {
		t.Fatal("stopped assignment disclosed secrets")
	}
}
func TestRunnerSecretLossFailsBeforeCompose(t *testing.T) {
	for _, damage := range []string{"missing", "invalid-value", "missing-key"} {
		t.Run(damage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runners.yaml")
			doc := secretDocument()
			first, err := ResolveRuntime(path, doc)
			if err != nil {
				t.Fatal(err)
			}
			files, err := filepath.Glob(filepath.Join(filepath.Dir(path), "runner-secrets", "*.json"))
			if err != nil || len(files) != 1 {
				t.Fatal(files, err)
			}
			if damage == "missing" {
				err = os.Remove(files[0])
			} else {
				raw, _ := os.ReadFile(files[0])
				var saved runnerSecrets
				if err = json.Unmarshal(raw, &saved); err != nil {
					t.Fatal(err)
				}
				if damage == "missing-key" {
					delete(saved.Values, "MASTER_KEY")
				} else {
					saved.Values["MASTER_KEY"] = "broken"
				}
				raw, _ = json.Marshal(saved)
				err = os.WriteFile(files[0], raw, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			runner := &stubRunner{}
			report := Apply(t.Context(), runner, path, doc)
			if len(runner.calls) != 0 || len(report.Services) != 1 || report.Services[0].Status != StatusFailed {
				t.Fatalf("secret loss reached compose: %+v %v", report, runner.calls)
			}
			raw, _ := json.Marshal(report)
			if strings.Contains(string(raw), first.Services[0].RuntimeBearer) {
				t.Fatal("secret leaked into failure")
			}
		})
	}
}
