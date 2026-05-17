package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

func (a *AuthConfig) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		a.Method = value.Value
		return nil
	case yaml.MappingNode:
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
