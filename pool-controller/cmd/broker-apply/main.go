// Command broker-apply stages the pool-controller-rendered broker host-config
// into the path the capability-broker reads its --config from, atomically.
//
// It is the first-class implementation of bootstrap.broker_apply_command,
// shipped inside the (distroless) pool-controller image so that staging needs
// no shell or coreutils in the container.
//
// pool-controller invokes it as:
//
//	broker-apply <target-config-path>
//
// piping the rendered YAML on stdin and setting these env vars:
//
//	POOL_CONTROLLER_BROKER_CONFIG_PATH      temp file holding the same YAML
//	POOL_CONTROLLER_BROKER_CONFIG_SHA256    sha256 of the YAML (integrity check)
//	POOL_CONTROLLER_BROKER_DESIRED_REVISION desired runtime revision (logging)
//
// The write is atomic (temp file in the target dir + rename + dir fsync), so a
// concurrently-reloading broker never observes a partial file.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "broker-apply:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: broker-apply <target-config-path>")
	}
	target := os.Args[1]

	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read rendered config from stdin: %w", err)
	}
	if len(data) == 0 {
		return fmt.Errorf("refusing to stage an empty broker config")
	}

	// Integrity check against the hash pool-controller computed for this render.
	if want := os.Getenv("POOL_CONTROLLER_BROKER_CONFIG_SHA256"); want != "" {
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != want {
			return fmt.Errorf("sha256 mismatch: got %s want %s", got, want)
		}
	}

	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".broker-apply-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("chmod temp: %w", err)
	}

	// Atomic replace: the broker only ever sees the old or the new file whole.
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpName, target, err)
	}

	// fsync the directory so the rename survives a crash.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}

	fmt.Printf("broker-apply: staged %d bytes to %s (revision %q)\n",
		len(data), target, os.Getenv("POOL_CONTROLLER_BROKER_DESIRED_REVISION"))
	return nil
}
