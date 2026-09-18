package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/ownership"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	listen := flag.String("listen", ":8443", "HTTPS listen address")
	data := flag.String("data-dir", "", "persistent ownership data directory")
	auth := flag.String("service-auth-file", "", "scoped credential file")
	cert := flag.String("tls-cert", "", "TLS certificate PEM")
	key := flag.String("tls-key", "", "TLS private key PEM")
	flag.Parse()
	if *auth == "" || *cert == "" || *key == "" {
		return fmt.Errorf("service-auth-file, tls-cert and tls-key are required")
	}
	store, err := repo.OpenOwnership(*data)
	if err != nil {
		return err
	}
	defer store.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	server := &http.Server{Addr: *listen, Handler: ownership.Handler(store, serviceauth.Verifier{Path: *auth}), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: time.Minute}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServeTLS(*cert, *key) }()
	select {
	case err := <-done:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdown, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	return server.Shutdown(shutdown)
}
