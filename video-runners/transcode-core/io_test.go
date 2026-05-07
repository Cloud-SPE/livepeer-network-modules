package transcode

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContentTypeForExt(t *testing.T) {
	tests := []struct {
		ext      string
		expected string
	}{
		{".mp4", "video/mp4"},
		{".MP4", "video/mp4"},
		{".mkv", "video/x-matroska"},
		{".webm", "video/webm"},
		{".mov", "video/quicktime"},
		{".avi", "video/x-msvideo"},
		{".ts", "video/mp2t"},
		{".m3u8", "application/vnd.apple.mpegurl"},
		{".m4s", "video/iso.segment"},
		{".xyz", "application/octet-stream"},
		{"", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			got := ContentTypeForExt(tt.ext)
			if got != tt.expected {
				t.Errorf("ContentTypeForExt(%q) = %q, want %q", tt.ext, got, tt.expected)
			}
		})
	}
}

func TestProgressReader(t *testing.T) {
	data := "hello world test data"
	reader := strings.NewReader(data)

	var lastTransferred int64
	var callCount int

	pr := &progressReader{
		reader: reader,
		total:  int64(len(data)),
		callback: func(transferred, total int64) {
			lastTransferred = transferred
			callCount++
		},
	}

	buf := make([]byte, 5)
	for {
		_, err := pr.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	if lastTransferred != int64(len(data)) {
		t.Errorf("final transferred = %d, want %d", lastTransferred, len(data))
	}
	if callCount == 0 {
		t.Error("callback was never called")
	}
}

func TestDownloadFile(t *testing.T) {
	content := "test video content for download"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Write([]byte(content))
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "downloaded.mp4")

	var progressCalled bool
	ct, err := DownloadFile(context.Background(), server.URL, destPath, func(transferred, total int64) {
		progressCalled = true
	})
	if err != nil {
		t.Fatalf("DownloadFile() error: %v", err)
	}

	if ct != "video/mp4" {
		t.Errorf("Content-Type = %q, want video/mp4", ct)
	}

	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(data) != content {
		t.Errorf("downloaded content = %q, want %q", string(data), content)
	}
	if !progressCalled {
		t.Error("progress callback was not called")
	}
}

func TestDownloadFile_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	_, err := DownloadFile(context.Background(), server.URL, filepath.Join(tmpDir, "out.mp4"), nil)
	if err == nil {
		t.Error("expected error for 404 response")
	}
}

func TestUploadFile(t *testing.T) {
	var receivedBody string
	var receivedContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		receivedContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "upload.mp4")
	content := "test upload content"
	os.WriteFile(srcPath, []byte(content), 0644)

	err := UploadFile(context.Background(), srcPath, server.URL, nil)
	if err != nil {
		t.Fatalf("UploadFile() error: %v", err)
	}

	if receivedBody != content {
		t.Errorf("server received %q, want %q", receivedBody, content)
	}
	if receivedContentType != "video/mp4" {
		t.Errorf("Content-Type = %q, want video/mp4", receivedContentType)
	}
}

func TestUploadFile_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "upload.mp4")
	os.WriteFile(srcPath, []byte("test"), 0644)

	err := UploadFile(context.Background(), srcPath, server.URL, nil)
	if err == nil {
		t.Error("expected error for 403 response")
	}
}
