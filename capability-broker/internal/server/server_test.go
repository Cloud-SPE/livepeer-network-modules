package server

import (
	"testing"
	"time"
)

func TestMetadataRefreshInterval_DefaultAndDisableSemantics(t *testing.T) {
	t.Parallel()

	if got := (&Server{}).metadataRefreshInterval(); got != 5*time.Minute {
		t.Fatalf("default interval = %v; want 5m", got)
	}

	if got := (&Server{opts: Options{MetadataRefreshInterval: 90 * time.Second}}).metadataRefreshInterval(); got != 90*time.Second {
		t.Fatalf("configured interval = %v; want 90s", got)
	}

	if got := (&Server{opts: Options{MetadataRefreshInterval: -1 * time.Second}}).metadataRefreshInterval(); got != 0 {
		t.Fatalf("disabled interval = %v; want 0", got)
	}
}
