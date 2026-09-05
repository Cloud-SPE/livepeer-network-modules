package main

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-member-agent/internal/attach"
)

// Bytes in on the edge come out at the runner that declared an ingest
// port, and back; a host with no such runner closes the connection.
// TLS is not under test here — serveRTMPS pipes whatever listener it
// is given, and the certificate path is the HTTP edge's.
func TestRTMPSEdgeForwardsToTheIngestRunner(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		for {
			c, err := upstream.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				line, _ := bufio.NewReader(c).ReadString('\n')
				_, _ = c.Write([]byte("echo:" + line))
			}()
		}
	}()
	_, portStr, _ := net.SplitHostPort(upstream.Addr().String())
	var port int
	for _, ch := range portStr {
		port = port*10 + int(ch-'0')
	}
	state := newRunnerState()
	state.set([]attach.Runner{{LocalID: "live", URL: "http://127.0.0.1:8080", RTMPPort: port}}, "r1")

	edge, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go serveRTMPS(ctx, edge, state)

	c, err := net.DialTimeout("tcp", edge.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	got, _ := bufio.NewReader(c).ReadString('\n')
	if got != "echo:hello\n" {
		t.Fatalf("through the edge = %q", got)
	}

	// The pool withdraws the ingest: a new connection is closed.
	state.set(nil, "r2")
	c2, err := net.DialTimeout("tcp", edge.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	_ = c2.SetDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	if n, err := c2.Read(buf); err == nil || n != 0 {
		t.Fatalf("expected the edge to close with no ingest runner, read %d %v", n, err)
	}
}
