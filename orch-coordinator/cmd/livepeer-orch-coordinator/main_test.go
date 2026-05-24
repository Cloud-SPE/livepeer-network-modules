package main

import (
	"log/slog"
	"strings"
	"testing"
)

func TestRun_RejectsInvalidSecureOrchURL(t *testing.T) {
	err := run(slog.Default(), bootConfig{
		listenAddr:    ":0",
		publicListen:  ":0",
		metricsListen: ":0",
		configPath:    "/does/not/matter",
		dataDir:       t.TempDir(),
		dev:           true,
		secureOrchURL: "ftp://secure.example.com",
	})
	if err == nil || !strings.Contains(err.Error(), "--secure-orch-url") {
		t.Fatalf("expected secure-orch-url validation error, got %v", err)
	}
}
