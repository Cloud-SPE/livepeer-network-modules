package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// AuthConfig describes how to authenticate to the backend.
//
// Accepts two YAML forms:
//   - bare scalar: `auth: none` → AuthConfig{Method: "none"}
//   - mapping:    `auth: { method: bearer, secret_ref: "vault://..." }`
type AuthConfig struct {
	Method    string `yaml:"method,omitempty"`
	SecretRef string `yaml:"secret_ref,omitempty"`
}

// UnmarshalYAML accepts both the scalar and mapping forms documented above.
func (a *AuthConfig) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		a.Method = value.Value
		return nil
	case yaml.MappingNode:
		// Decode via a type alias to avoid recursing back into this method.
		type rawAuthConfig AuthConfig
		var raw rawAuthConfig
		if err := value.Decode(&raw); err != nil {
			return err
		}
		*a = AuthConfig(raw)
		return nil
	default:
		return fmt.Errorf("auth: expected string or mapping (got node kind %v)", value.Kind)
	}
}
