// session-runner-stub is the conformance stub session-runner. It
// answers SessionRunnerControl.Health and Envelopes / Frames over the
// gRPC unix socket the broker hands it via the
// LIVEPEER_SESSION_RUNNER_SOCK env variable.
//
// Behaviors selected by LIVEPEER_STUB_BEHAVIOR:
//
//   - "echo"                  — the default. Echoes every workload
//                               envelope back as `<type>.echo`.
//   - "burst"                 — emits envelopes at
//                               LIVEPEER_STUB_BURST_RATE_HZ until the
//                               broker stops reading.
//   - "tick"                  — emits a session.usage.tick every
//                               LIVEPEER_STUB_TICK_INTERVAL_MS.
//   - "crash-after-startup"   — sleeps LIVEPEER_STUB_CRASH_DELAY_MS,
//                               then os.Exit(1).
//
// Q6 lock: this stub lives at
// livepeer-network-protocol/conformance/runner/session-runner-stub/.
package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc"

	srpb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/sessionrunner/v1"
)

func main() {
	socketPath := os.Getenv("LIVEPEER_SESSION_RUNNER_SOCK")
	if socketPath == "" {
		log.Fatal("LIVEPEER_SESSION_RUNNER_SOCK env var is required")
	}
	behavior := os.Getenv("LIVEPEER_STUB_BEHAVIOR")
	if behavior == "" {
		behavior = "echo"
	}

	_ = os.Remove(socketPath)
	lis, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("listen %s: %v", socketPath, err)
	}

	srv := grpc.NewServer()
	stub := &stubServer{behavior: behavior}
	srpb.RegisterSessionRunnerControlServer(srv, stub)
	srpb.RegisterSessionRunnerMediaServer(srv, stub)

	if behavior == "crash-after-startup" {
		ms, _ := strconv.Atoi(os.Getenv("LIVEPEER_STUB_CRASH_DELAY_MS"))
		if ms <= 0 {
			ms = 2000
		}
		go func() {
			time.Sleep(time.Duration(ms) * time.Millisecond)
			log.Fatalf("session-runner-stub: simulated crash after %dms", ms)
		}()
	}

	log.Printf("session-runner-stub: behavior=%s socket=%s", behavior, socketPath)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

type stubServer struct {
	srpb.UnimplementedSessionRunnerControlServer
	srpb.UnimplementedSessionRunnerMediaServer
	behavior string

	mu        sync.Mutex
	envelopes []*srpb.ControlEnvelope
}

func (s *stubServer) Health(_ context.Context, _ *srpb.HealthRequest) (*srpb.HealthResponse, error) {
	return &srpb.HealthResponse{Status: "ready"}, nil
}

func (s *stubServer) Shutdown(_ context.Context, _ *srpb.ShutdownRequest) (*srpb.ShutdownResponse, error) {
	go func() {
		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	}()
	return &srpb.ShutdownResponse{}, nil
}

func (s *stubServer) Envelopes(stream srpb.SessionRunnerControl_EnvelopesServer) error {
	switch s.behavior {
	case "burst":
		return s.runBurst(stream)
	case "tick":
		return s.runTick(stream)
	default:
		return s.runEcho(stream)
	}
}

func (s *stubServer) runEcho(stream srpb.SessionRunnerControl_EnvelopesServer) error {
	for {
		env, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		s.mu.Lock()
		s.envelopes = append(s.envelopes, env)
		s.mu.Unlock()
		out := &srpb.ControlEnvelope{
			Type: env.GetType() + ".echo",
			Body: env.GetBody(),
		}
		if err := stream.Send(out); err != nil {
			return err
		}
	}
}

func (s *stubServer) runBurst(stream srpb.SessionRunnerControl_EnvelopesServer) error {
	rateHz, _ := strconv.Atoi(os.Getenv("LIVEPEER_STUB_BURST_RATE_HZ"))
	if rateHz <= 0 {
		rateHz = 1000
	}
	interval := time.Second / time.Duration(rateHz)
	if interval <= 0 {
		interval = time.Microsecond
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	go func() {
		for {
			if _, err := stream.Recv(); err != nil {
				return
			}
		}
	}()
	for range t.C {
		err := stream.Send(&srpb.ControlEnvelope{Type: "burst.payload", Body: []byte(`{"x":1}`)})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *stubServer) runTick(stream srpb.SessionRunnerControl_EnvelopesServer) error {
	ms, _ := strconv.Atoi(os.Getenv("LIVEPEER_STUB_TICK_INTERVAL_MS"))
	if ms <= 0 {
		ms = 100
	}
	t := time.NewTicker(time.Duration(ms) * time.Millisecond)
	defer t.Stop()
	go func() {
		for {
			if _, err := stream.Recv(); err != nil {
				return
			}
		}
	}()
	for range t.C {
		err := stream.Send(&srpb.ControlEnvelope{Type: "session.usage.tick", Body: []byte(`{"units":1}`)})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *stubServer) ReportWorkUnits(_ *srpb.WorkUnitReportRequest, stream srpb.SessionRunnerControl_ReportWorkUnitsServer) error {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		if err := stream.Send(&srpb.WorkUnitReport{Delta: 1}); err != nil {
			return err
		}
	}
	return nil
}

func (s *stubServer) Frames(stream srpb.SessionRunnerMedia_FramesServer) error {
	for {
		frame, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		out := &srpb.MediaFrame{
			Direction:   srpb.Direction_DIRECTION_EGRESS,
			TrackId:     frame.GetTrackId(),
			PayloadType: frame.GetPayloadType(),
			Rtp:         frame.GetRtp(),
		}
		if err := stream.Send(out); err != nil {
			return err
		}
	}
}
