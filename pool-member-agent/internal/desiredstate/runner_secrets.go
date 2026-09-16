package desiredstate

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

var runnerSecretName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

type runnerSecrets struct {
	EnrollmentID string            `json:"enrollment_id"`
	Service      string            `json:"service"`
	Values       map[string]string `json:"values"`
	Keys         []string          `json:"keys"`
}

// ResolveRuntime provisions only host-local secrets, before any new container or
// tunnel route is installed. Every value is a stable random base64-encoded 256-bit
// secret. The controller sends names, never values. No failure generates a
// replacement for a corrupt/missing-key record, which would orphan encrypted data.
func ResolveRuntime(composePath string, doc Document) (Document, error) {
	doc.Services = append([]Service(nil), doc.Services...)
	for i := range doc.Services {
		s := &doc.Services[i]
		if !s.Stop && s.LocalBearerEnv != "" && len(s.SecretEnv) == 0 {
			return doc, errors.New("local runner bearer declaration missing")
		}
		if s.runtimeResolved || s.Stop || len(s.SecretEnv) == 0 {
			continue
		}
		if doc.EnrollmentID == "" || s.Name == "" {
			return doc, errors.New("runner secret identity missing")
		}
		values, err := loadOrCreateRunnerSecrets(filepath.Join(filepath.Dir(composePath), "runner-secrets"), doc.EnrollmentID, *s)
		if err != nil {
			return doc, err
		}
		for name, value := range values {
			s.ComposeFragment = strings.ReplaceAll(s.ComposeFragment, "{{secret."+name+"}}", value)
		}
		if strings.Contains(s.ComposeFragment, "{{secret.") {
			return doc, errors.New("unresolved runner secret declaration")
		}
		if s.LocalBearerEnv != "" {
			s.RuntimeBearer = values[s.LocalBearerEnv]
			if s.RuntimeBearer == "" {
				return doc, errors.New("runner bearer secret missing")
			}
		}
		s.runtimeResolved = true
	}
	return doc, nil
}

func loadOrCreateRunnerSecrets(dir, enrollment string, s Service) (map[string]string, error) {
	expected := map[string]bool{}
	for _, name := range s.SecretEnv {
		if !runnerSecretName.MatchString(name) || expected[name] {
			return nil, errors.New("invalid runner secret declaration")
		}
		expected[name] = true
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("runner secrets require a real directory")
	}
	sum := sha256.Sum256([]byte(enrollment + "\x00" + s.Name))
	path := filepath.Join(dir, hex.EncodeToString(sum[:])+".json")
	lock, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	record := runnerSecrets{EnrollmentID: enrollment, Service: s.Name, Values: map[string]string{}}
	info, err := os.Lstat(path)
	exists := err == nil
	marker := path + ".initialized"
	if !exists {
		if _, markErr := os.Stat(marker); markErr == nil {
			return nil, errors.New("runner secret record missing; restore it before retrying")
		} else if !os.IsNotExist(markErr) {
			return nil, markErr
		}
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if exists {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return nil, errors.New("runner secret record must be private and regular")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if len(raw) > 65536 || json.Unmarshal(raw, &record) != nil || record.EnrollmentID != enrollment || record.Service != s.Name {
			return nil, errors.New("invalid runner secret record; restore it before retrying")
		}
	}
	if exists && len(record.Keys) == 0 {
		return nil, errors.New("runner secret key inventory missing; restore it before retrying")
	}
	for _, name := range record.Keys {
		value, ok := record.Values[name]
		decoded, err := base64.StdEncoding.DecodeString(value)
		if !ok || err != nil || len(decoded) != 32 {
			return nil, errors.New("runner secret record lost a key; restore it before retrying")
		}
	}
	// Adding a new declared key is safe; losing an existing recorded value is not.
	for _, name := range s.SecretEnv {
		if value, ok := record.Values[name]; ok {
			decoded, err := base64.StdEncoding.DecodeString(value)
			if err != nil || len(decoded) != 32 {
				return nil, errors.New("invalid runner secret value; restore it before retrying")
			}
			continue
		}
		var secret [32]byte
		if _, err = rand.Read(secret[:]); err != nil {
			return nil, err
		}
		if record.Values == nil {
			return nil, errors.New("runner secret values missing; restore it before retrying")
		}
		record.Values[name] = base64.StdEncoding.EncodeToString(secret[:])
		record.Keys = append(record.Keys, name)
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	if err = writePrivateAtomic(path, raw); err != nil {
		return nil, err
	}
	if err = writePrivateAtomic(marker, []byte("initialized\n")); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for name := range expected {
		out[name] = record.Values[name]
	}
	return out, nil
}

func writePrivateAtomic(path string, raw []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".private-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if _, err = temp.Write(raw); err != nil {
		return err
	}
	if err = temp.Sync(); err != nil {
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	if err = os.Rename(temp.Name(), path); err != nil {
		return err
	}
	parent, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open private state directory: %w", err)
	}
	defer parent.Close()
	return parent.Sync()
}
