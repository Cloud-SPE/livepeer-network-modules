package backend

import (
	"fmt"
	"os"
	"strings"
)

// EnvSecretResolver resolves "env://VAR_NAME" references against the process
// environment. v0.1 supports this scheme only; "vault://" lookups return an
// error indicating the integration is not yet wired (planned for plan 0005
// alongside real payment-daemon integration).
type EnvSecretResolver struct{}

// NewEnvSecretResolver returns a resolver that handles env:// references.
func NewEnvSecretResolver() *EnvSecretResolver { return &EnvSecretResolver{} }

// Resolve looks up the reference and returns its plain-text value.
func (e *EnvSecretResolver) Resolve(ref string) (string, error) {
	switch {
	case strings.HasPrefix(ref, "env://"):
		name := strings.TrimPrefix(ref, "env://")
		val, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("env secret %q not set", name)
		}
		return val, nil
	case strings.HasPrefix(ref, "vault://"):
		return "", fmt.Errorf("secret reference %q: vault:// integration is not yet wired (planned: plan 0005)", ref)
	default:
		return "", fmt.Errorf("secret reference scheme not supported: %q", ref)
	}
}

// Compile-time interface check.
var _ SecretResolver = (*EnvSecretResolver)(nil)
