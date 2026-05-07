// livepeer-capability-broker is the Go reference implementation of the
// workload-agnostic capability broker per the spec at
// livepeer-network-protocol/.
//
// See capability-broker/docs/design-docs/architecture.md for the planned
// package layout and request lifecycle.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/media/encoder"
	mediartmp "github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/media/rtmp"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/observability"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/server"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/server/middleware"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

func main() {
	var (
		configPath  = flag.String("config", "/etc/livepeer/host-config.yaml", "path to host-config.yaml")
		listenAddr  = flag.String("listen", "", "HTTP listen address (overrides config)")
		metricsAddr = flag.String("metrics", "", "Prometheus metrics listen address (overrides config)")
		showVersion = flag.Bool("version", false, "print version and exit")

		// Plan 0015 — interim-debit cadence flags.
		interimDebitInterval = flag.Duration(
			"interim-debit-interval",
			30*time.Second,
			"interim-debit tick cadence for long-running sessions; 0 disables the ticker entirely (plan 0015)",
		)
		interimDebitMinRunwayUnits = flag.Uint64(
			"interim-debit-min-runway-units",
			60,
			"minimum required runway in work-units passed to PayeeDaemon.SufficientBalance per tick (plan 0015)",
		)
		interimDebitGraceOnInsufficient = flag.Duration(
			"interim-debit-grace-on-insufficient",
			0,
			"grace period before terminating a handler after SufficientBalance returns false; "+
				"reserved for the future mid-session top-up flow (plan 0015)",
		)

		rtmpListenAddr = flag.String(
			"rtmp-listen-addr",
			"",
			"RTMP ingest bind (e.g. :1935); empty disables the RTMP listener",
		)
		rtmpMaxConcurrent = flag.Uint(
			"rtmp-max-concurrent-streams",
			100,
			"per-broker cap on concurrent RTMP publish sessions",
		)
		rtmpIdleTimeout = flag.Duration(
			"rtmp-idle-timeout",
			10*time.Second,
			"per-stream idle timeout once a publish handshake has completed",
		)
		rtmpOnDuplicateKey = flag.String(
			"rtmp-on-duplicate-key",
			"reject",
			"policy on duplicate stream-key publishes: reject | replace",
		)
		rtmpRequireStreamKey = flag.Bool(
			"rtmp-require-stream-key",
			true,
			"reject RTMP publishes without a stream-key suffix; off for fixture / dev only",
		)

		ffmpegBinary = flag.String(
			"ffmpeg-binary",
			"ffmpeg",
			"path to the ffmpeg binary",
		)
		ffmpegCancelGrace = flag.Duration(
			"ffmpeg-cancel-grace",
			5*time.Second,
			"SIGTERM-to-SIGKILL grace window for the per-session FFmpeg subprocess",
		)

		encoderFlag = flag.String(
			"encoder",
			"auto",
			"encoder selection: auto | nvenc | qsv | vaapi | libx264",
		)
		encoderAllowCPU = flag.Bool(
			"encoder-allow-cpu",
			false,
			"permit libx264 fallback when --encoder=auto finds no GPU (production deployments should use a GPU)",
		)

		hlsLegacy = flag.Bool(
			"hls-legacy",
			false,
			"emit HLS v3 + mpegts segments instead of LL-HLS fmp4 (~12-24s glass-to-glass; use only for older Android players)",
		)
		hlsPartDuration = flag.Duration(
			"hls-part-duration",
			333*time.Millisecond,
			"LL-HLS #EXT-X-PART duration; ignored when --hls-legacy=true",
		)
		hlsSegmentDuration = flag.Duration(
			"hls-segment-duration",
			2*time.Second,
			"FFmpeg -hls_time (LL-HLS default 2s; legacy uses 6s)",
		)
		hlsPlaylistWindow = flag.Uint(
			"hls-playlist-window",
			4,
			"FFmpeg -hls_list_size (LL-HLS default 4; legacy uses 5)",
		)
		hlsScratchDir = flag.String(
			"hls-scratch-dir",
			"/var/lib/livepeer/rtmp-hls",
			"per-session HLS scratch root (operator should mount a tmpfs)",
		)
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("livepeer-capability-broker", version)
		return
	}

	observability.SetupLogger()
	log.Printf("livepeer-capability-broker %s", version)

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config load failed: %v", err)
	}
	log.Printf("config loaded from %s; %d capabilities declared", *configPath, len(cfg.Capabilities))

	if *listenAddr != "" {
		cfg.Listen.Paid = *listenAddr
	}
	if *metricsAddr != "" {
		cfg.Listen.Metrics = *metricsAddr
	}

	dupPolicy := mediartmp.DuplicatePolicy(*rtmpOnDuplicateKey)
	switch dupPolicy {
	case mediartmp.DuplicateReject, mediartmp.DuplicateReplace:
	default:
		log.Fatalf("--rtmp-on-duplicate-key=%q must be 'reject' or 'replace'", *rtmpOnDuplicateKey)
	}

	var probe encoder.ProbeResult
	if *rtmpListenAddr != "" {
		want, err := encoder.ParseCodec(*encoderFlag)
		if err != nil {
			log.Fatalf("--encoder: %v", err)
		}
		probe, err = encoder.Probe(encoder.ProbeOptions{
			Want:     want,
			AllowCPU: *encoderAllowCPU,
			Bin:      *ffmpegBinary,
		})
		if err != nil {
			log.Fatalf("encoder probe: %v", err)
		}
		log.Printf("encoder: selected=%s available=%v", probe.Selected, probe.Available)
	}

	srv, err := server.New(cfg, server.Options{
		InterimDebit: middleware.InterimDebitConfig{
			Interval:            *interimDebitInterval,
			MinRunwayUnits:      *interimDebitMinRunwayUnits,
			GraceOnInsufficient: *interimDebitGraceOnInsufficient,
		},
		RTMP: server.RTMPOptions{
			Addr:             *rtmpListenAddr,
			MaxConcurrent:    int(*rtmpMaxConcurrent),
			IdleTimeout:      *rtmpIdleTimeout,
			DuplicatePolicy:  dupPolicy,
			RequireStreamKey: *rtmpRequireStreamKey,
		},
		FFmpeg: server.FFmpegOptions{
			Binary:      *ffmpegBinary,
			CancelGrace: *ffmpegCancelGrace,
			Codec:       probe.Selected,
		},
		HLS: server.HLSOptions{
			Legacy:          *hlsLegacy,
			PartDuration:    *hlsPartDuration,
			SegmentDuration: *hlsSegmentDuration,
			PlaylistWindow:  int(*hlsPlaylistWindow),
			ScratchDir:      *hlsScratchDir,
		},
	})
	if err != nil {
		log.Fatalf("server init failed: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := srv.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("server error: %v", err)
	}
	log.Println("shutdown complete")
	_ = os.Stdout.Sync()
}
