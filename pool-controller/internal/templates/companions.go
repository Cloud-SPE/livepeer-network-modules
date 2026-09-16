package templates

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

var secretEnvNameRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

var containerNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
var serviceRefRE = regexp.MustCompile(`\{\{service\.([a-z][a-z0-9-]*)\}\}`)

func validateCaches(caches map[string]string) error {
	var targets []string
	for name, target := range caches {
		if !containerNameRE.MatchString(name) || !path.IsAbs(target) || path.Clean(target) != target || target == "/" || strings.ContainsAny(target, "\n\r\x00") {
			return fmt.Errorf("invalid runner cache name/path")
		}
		for _, reserved := range []string{"/dev", "/proc", "/sys"} {
			if target == reserved || strings.HasPrefix(target, reserved+"/") {
				return fmt.Errorf("runner cache cannot mask devices or kernel files")
			}
		}
		for _, existing := range targets {
			if target == existing || strings.HasPrefix(target, existing+"/") || strings.HasPrefix(existing, target+"/") {
				return fmt.Errorf("overlapping runner cache mounts")
			}
		}
		targets = append(targets, target)
	}
	return nil
}

func (t Template) validateCompanions() error {
	if len(t.RunnerCompose.SecretEnv) > 0 && (len(t.RunnerCompose.Image) == 0 || t.RunnerCompose.InternalURL != "") {
		return fmt.Errorf("runner secrets require a managed primary image")
	}
	secretNames := map[string]bool{}
	for _, name := range t.RunnerCompose.SecretEnv {
		if !secretEnvNameRE.MatchString(name) || secretNames[name] {
			return fmt.Errorf("invalid or duplicate runner secret environment")
		}
		if _, exists := t.RunnerCompose.Env[name]; exists {
			return fmt.Errorf("runner secret conflicts with literal environment")
		}
		if name == "LIVEPEER_PUBLIC_URL" || name == "LIVEPEER_PUBLIC_RTMP_URL" || t.RunnerCompose.HostAdmission != nil && name == t.RunnerCompose.HostAdmission.EnvVar {
			return fmt.Errorf("runner secret conflicts with generated environment")
		}
		secretNames[name] = true
	}
	if t.RunnerCompose.LocalBearerEnv != "" && !secretNames[t.RunnerCompose.LocalBearerEnv] {
		return fmt.Errorf("local bearer must reference a generated runner secret")
	}

	if err := validateCaches(t.RunnerCompose.Caches); err != nil {
		return err
	}
	if len(t.RunnerCompose.Companions) > 0 && (len(t.RunnerCompose.Image) == 0 || t.RunnerCompose.InternalURL != "") {
		return fmt.Errorf("template %s: companions require a primary runner image", t.ID)
	}
	names := map[string]bool{"main": true}
	for _, c := range t.RunnerCompose.Companions {
		if !containerNameRE.MatchString(c.Name) || names[c.Name] {
			return fmt.Errorf("template %s: invalid/duplicate companion name", t.ID)
		}
		names[c.Name] = true
		if len(c.Image) == 0 {
			return fmt.Errorf("template %s: companion image required", t.ID)
		}
		if err := validateCaches(c.Caches); err != nil {
			return err
		}
		// Reuse the vendor/class image validation without allowing nested groups.
		copy := t
		copy.RunnerCompose = RunnerCompose{Image: c.Image}
		if err := copy.Validate(); err != nil {
			return fmt.Errorf("companion %s: %w", c.Name, err)
		}
	}
	values := append([]string(nil), t.RunnerCompose.Command...)
	for _, value := range t.RunnerCompose.Env {
		values = append(values, value)
	}
	for _, c := range t.RunnerCompose.Companions {
		values = append(values, c.Command...)
		for _, value := range c.Env {
			values = append(values, value)
		}
	}
	for _, value := range values {
		if strings.Contains(value, "{{secret.") {
			return fmt.Errorf("secret placeholders are agent-owned; use secret_env declarations")
		}
		for _, match := range serviceRefRE.FindAllStringSubmatch(value, -1) {
			if !names[match[1]] {
				return fmt.Errorf("unknown assignment service %s", match[1])
			}
		}
		if strings.Contains(serviceRefRE.ReplaceAllString(value, ""), "{{service.") {
			return fmt.Errorf("invalid assignment service reference")
		}
	}
	return nil
}
