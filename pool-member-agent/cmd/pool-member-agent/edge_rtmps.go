package main

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// The RTMPS half of the member's edge (plan 0046 §2.7).
//
// RTMP is not HTTP, so the reverse proxy in edge.go cannot carry it,
// and an ingest has to be reachable by the caller directly. The
// member publishes one RTMPS port; the agent terminates TLS on it with
// the same certificate the HTTP edge uses and forwards the raw stream
// to the one runner that declared an rtmp_port. One port per host is
// enough: the live class's stance is one template per card, and the
// media router inside the runner multiplexes streams by key. The
// runner speaks plain RTMP inside the host and advertises
// LIVEPEER_PUBLIC_RTMP_URL, which the pool set from the host's
// public_url and this port.

// rtmpTarget is the runner the RTMPS edge forwards to: the first with
// an rtmp_port, addressed by its service name on the compose network.
func rtmpTarget(state *runnerState) (string, bool) {
	state.mu.RLock()
	defer state.mu.RUnlock()
	for _, r := range state.runners {
		if r.RTMPPort <= 0 {
			continue
		}
		host := r.LocalID
		if u, err := url.Parse(r.URL); err == nil && u.Hostname() != "" {
			host = u.Hostname()
		}
		return net.JoinHostPort(host, strconv.Itoa(r.RTMPPort)), true
	}
	return "", false
}

// serveRTMPS accepts TLS connections on ln and pipes each to the
// current RTMP target. Resolved per connection, so a runner the pool
// places after the edge started is forwarded to without a restart.
func serveRTMPS(ctx context.Context, ln net.Listener, state *runnerState) {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("rtmps edge: accept: %v", err)
			continue
		}
		go func() {
			defer conn.Close()
			target, ok := rtmpTarget(state)
			if !ok {
				// No ingest on this host right now: close, as a port
				// nothing listens on would.
				return
			}
			up, err := net.DialTimeout("tcp", target, 5*time.Second)
			if err != nil {
				log.Printf("rtmps edge: %s unreachable: %v", target, err)
				return
			}
			defer up.Close()
			done := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(up, conn); done <- struct{}{} }()
			go func() { _, _ = io.Copy(conn, up); done <- struct{}{} }()
			<-done
		}()
	}
}

// startRTMPSEdge listens when the host is public, on
// LIVEPEER_EDGE_RTMPS_LISTEN (default :1936), with the HTTP edge's
// certificate.
func startRTMPSEdge(ctx context.Context, state *runnerState, cert tls.Certificate) error {
	if strings.TrimSpace(os.Getenv("LIVEPEER_PUBLIC_URL")) == "" {
		return nil
	}
	listen := envOr("LIVEPEER_EDGE_RTMPS_LISTEN", ":1936")
	ln, err := tls.Listen("tcp", listen, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		return errors.New("RTMPS EDGE CANNOT START: " + err.Error())
	}
	log.Printf("rtmps edge listening on %s", listen)
	go serveRTMPS(ctx, ln, state)
	return nil
}
