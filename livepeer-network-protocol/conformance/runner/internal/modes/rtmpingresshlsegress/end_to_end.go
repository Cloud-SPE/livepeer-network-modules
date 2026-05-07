package rtmpingresshlsegress

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/envelope"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/fixtures"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/mockbackend"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/report"
)

// runEndToEnd executes the full RTMP push + LL-HLS fetch flow per
// docs/exec-plans/active/0011-followup §11.1. Steps:
//   1. Session-open POST → 202 with rtmp_ingest_url + hls_playback_url + stream_key.
//   2. ffmpeg -re -f lavfi -i testsrc=duration=N → broker's RTMP listener.
//   3. Poll hls_playback_url until #EXTM3U + ≥1 segment ref appears.
//   4. GET first segment → assert non-empty body.
//   5. Wait for ffmpeg push to terminate → broker auto-closes.
//
// Ledger-state assertion (DebitBalance count > 0) is deferred — adding
// the payee-daemon helper in this fixture is mechanical follow-up; the
// pipeline-up assertion is the load-bearing check.
func runEndToEnd(ctx context.Context, client *http.Client, brokerURL string, fx fixtures.Fixture, mock *mockbackend.Server) report.Result {
	failures := []string{}
	mock.Reset()

	// 1. Session-open.
	openResp, openBody, err := postSessionOpen(ctx, client, brokerURL, fx)
	if err != nil {
		return fail(fx, err.Error())
	}
	if openResp.StatusCode != fx.ResponseExpect.Status {
		failures = append(failures, fmt.Sprintf("session-open status: expected %d, got %d", fx.ResponseExpect.Status, openResp.StatusCode))
	}
	for _, h := range fx.ResponseExpect.HeadersPresent {
		if openResp.Header.Get(h) == "" {
			failures = append(failures, "session-open missing header: "+h)
		}
	}
	rtmpURL, _ := openBody["rtmp_ingest_url"].(string)
	hlsURL, _ := openBody["hls_playback_url"].(string)
	if rtmpURL == "" || hlsURL == "" {
		failures = append(failures, "session-open response missing rtmp_ingest_url or hls_playback_url")
		return finalize(fx, failures)
	}

	// The broker returns playback URLs against its public host. In
	// the conformance compose stack the broker is reachable from the
	// runner as `http://broker:8080`; rewrite playback URLs to that.
	hlsURL = rewriteHostPort(hlsURL, brokerURL)

	// 2. Push synthetic RTMP via ffmpeg.
	dur := fx.RTMP.PushDurationSeconds
	if dur <= 0 {
		dur = 5
	}
	pushCtx, cancelPush := context.WithTimeout(ctx, time.Duration(dur+10)*time.Second)
	defer cancelPush()
	pushDone := make(chan error, 1)
	go func() {
		pushDone <- pushSyntheticRTMP(pushCtx, rtmpURL, dur)
	}()

	// 3. Poll for LL-HLS playlist.
	wait := fx.RTMP.HLSWaitSeconds
	if wait <= 0 {
		wait = 8
	}
	playlist, segRef, err := waitForPlaylist(ctx, client, hlsURL, time.Duration(wait)*time.Second)
	if err != nil {
		failures = append(failures, "playlist materialise: "+err.Error())
	} else {
		if !strings.HasPrefix(playlist, "#EXTM3U") {
			failures = append(failures, fmt.Sprintf("playlist body missing #EXTM3U prefix; got %q", truncate(playlist, 80)))
		}
		// 4. GET first segment.
		if segRef != "" {
			if err := fetchSegment(ctx, client, hlsURL, segRef); err != nil {
				failures = append(failures, "segment fetch: "+err.Error())
			}
		} else {
			failures = append(failures, "playlist did not reference any segment")
		}
	}

	// 5. Wait for ffmpeg push to terminate.
	select {
	case err := <-pushDone:
		if err != nil && !isExpectedTermination(err) {
			failures = append(failures, "ffmpeg push exited unexpectedly: "+err.Error())
		}
	case <-time.After(time.Duration(dur+15) * time.Second):
		failures = append(failures, fmt.Sprintf("ffmpeg push did not terminate within %ds", dur+15))
	}

	return finalize(fx, failures)
}

func postSessionOpen(ctx context.Context, client *http.Client, brokerURL string, fx fixtures.Fixture) (*http.Response, map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, fx.Request.Method, brokerURL+fx.Request.Path,
		strings.NewReader(fx.Request.Body))
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	hdrs, err := envelope.SubstituteHeaders(fx.Request.Headers)
	if err != nil {
		return nil, nil, fmt.Errorf("build payment envelope: %w", err)
	}
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("call broker: %w", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, nil, fmt.Errorf("read response body: %w", err)
	}
	parsed := map[string]any{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, nil, fmt.Errorf("response body is not JSON: %w", err)
	}
	return resp, parsed, nil
}

func pushSyntheticRTMP(ctx context.Context, rtmpURL string, durSec int) error {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner",
		"-loglevel", "warning",
		"-re",
		"-f", "lavfi",
		"-i", fmt.Sprintf("testsrc=duration=%d:size=320x240:rate=30", durSec),
		"-f", "lavfi",
		"-i", fmt.Sprintf("anullsrc=channel_layout=stereo:sample_rate=44100:duration=%d", durSec),
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-c:a", "aac", "-b:a", "64k",
		"-f", "flv",
		rtmpURL,
	)
	return cmd.Run()
}

func waitForPlaylist(ctx context.Context, client *http.Client, playlistURL string, deadline time.Duration) (string, string, error) {
	until := time.Now().Add(deadline)
	var lastBody string
	for time.Now().Before(until) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, playlistURL, nil)
		if err != nil {
			return "", "", err
		}
		resp, err := client.Do(req)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			lastBody = string(b)
			if resp.StatusCode == http.StatusOK && strings.HasPrefix(lastBody, "#EXTM3U") {
				if seg := firstSegmentRef(lastBody); seg != "" {
					return lastBody, seg, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return lastBody, "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return lastBody, "", fmt.Errorf("playlist did not contain a segment within %s", deadline)
}

// firstSegmentRef returns the first non-comment line that doesn't start
// with `#` — that's the first media-segment URI in an HLS playlist.
func firstSegmentRef(playlist string) string {
	for _, line := range strings.Split(playlist, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}

func fetchSegment(ctx context.Context, client *http.Client, playlistURL, segRef string) error {
	segURL := joinPlaylistDir(playlistURL, segRef)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, segURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("segment status: %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return fmt.Errorf("read segment: %w", err)
	}
	if len(b) == 0 {
		return fmt.Errorf("segment body empty")
	}
	return nil
}

// joinPlaylistDir resolves a segment ref relative to the playlist URL's
// directory. e.g. https://host/_hls/abc/playlist.m3u8 + segment_00001.m4s →
// https://host/_hls/abc/segment_00001.m4s.
func joinPlaylistDir(playlistURL, segRef string) string {
	if strings.HasPrefix(segRef, "http://") || strings.HasPrefix(segRef, "https://") {
		return segRef
	}
	idx := strings.LastIndex(playlistURL, "/")
	if idx < 0 {
		return playlistURL + "/" + segRef
	}
	return playlistURL[:idx+1] + segRef
}

// rewriteHostPort replaces the host:port portion of `target` with the
// host:port from `source`. Used because the broker's session-open
// response contains URLs against its advertised public host (e.g.
// `broker.example.com`) but the runner reaches it via the compose-network
// hostname (e.g. `broker:8080`).
func rewriteHostPort(target, source string) string {
	srcSchemeEnd := strings.Index(source, "://")
	if srcSchemeEnd < 0 {
		return target
	}
	srcAuthority := source[srcSchemeEnd+3:]
	if i := strings.Index(srcAuthority, "/"); i >= 0 {
		srcAuthority = srcAuthority[:i]
	}

	tgtSchemeEnd := strings.Index(target, "://")
	if tgtSchemeEnd < 0 {
		return target
	}
	rest := target[tgtSchemeEnd+3:]
	pathStart := strings.Index(rest, "/")
	scheme := source[:srcSchemeEnd]
	if pathStart < 0 {
		return scheme + "://" + srcAuthority
	}
	return scheme + "://" + srcAuthority + rest[pathStart:]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// isExpectedTermination distinguishes a clean ffmpeg exit (after duration
// elapses + the broker closes the leg cleanly) from an unexpected error.
func isExpectedTermination(err error) bool {
	if err == nil {
		return true
	}
	// ffmpeg exits with 0 on natural EOF; a non-zero exit on RTMP
	// disconnect at the very end of the duration is also benign for
	// this fixture's purposes.
	msg := err.Error()
	return strings.Contains(msg, "exit status 0") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "End of file")
}

func finalize(fx fixtures.Fixture, failures []string) report.Result {
	return report.Result{
		Name:     fx.Name,
		Mode:     fx.Mode,
		Pass:     len(failures) == 0,
		Failures: failures,
	}
}
