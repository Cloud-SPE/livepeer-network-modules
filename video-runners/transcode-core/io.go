package transcode

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ProgressFunc is called during downloads and uploads with progress information.
type ProgressFunc func(bytesTransferred, totalBytes int64)

// DownloadFile downloads a file from the given URL to destPath, reporting progress.
// Returns the Content-Type header value.
func DownloadFile(ctx context.Context, url, destPath string, progress ProgressFunc) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create download request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	totalBytes := resp.ContentLength

	f, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("create file %s: %w", destPath, err)
	}
	defer f.Close()

	reader := io.Reader(resp.Body)
	if progress != nil {
		reader = &progressReader{
			reader:   resp.Body,
			total:    totalBytes,
			callback: progress,
		}
	}

	if _, err := io.Copy(f, reader); err != nil {
		return "", fmt.Errorf("write to %s: %w", destPath, err)
	}

	return contentType, nil
}

// UploadFile uploads a file to the given pre-signed URL via HTTP PUT.
func UploadFile(ctx context.Context, srcPath, uploadURL string, progress ProgressFunc) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", srcPath, err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", srcPath, err)
	}

	var body io.Reader = f
	if progress != nil {
		body = &progressReader{
			reader:   f,
			total:    stat.Size(),
			callback: progress,
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, body)
	if err != nil {
		return fmt.Errorf("create upload request: %w", err)
	}

	req.ContentLength = stat.Size()
	req.Header.Set("Content-Type", ContentTypeForExt(filepath.Ext(srcPath)))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("upload to %s: %w", uploadURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upload to %s: HTTP %d", uploadURL, resp.StatusCode)
	}

	return nil
}

// ContentTypeForExt returns the MIME type for a file extension.
func ContentTypeForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".mp4":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".avi":
		return "video/x-msvideo"
	case ".ts":
		return "video/mp2t"
	case ".m3u8":
		return "application/vnd.apple.mpegurl"
	case ".m4s":
		return "video/iso.segment"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	default:
		return "application/octet-stream"
	}
}

// progressReader wraps an io.Reader and calls a callback with progress updates.
type progressReader struct {
	reader      io.Reader
	total       int64
	transferred int64
	callback    ProgressFunc
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	if n > 0 {
		pr.transferred += int64(n)
		if pr.callback != nil {
			pr.callback(pr.transferred, pr.total)
		}
	}
	return n, err
}
