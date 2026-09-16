package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/member-portal/internal/portal"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/memberauth"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	config := flag.String("config", "/etc/member-portal/config.yaml", "configuration path")
	validate := flag.Bool("validate-config", false, "validate configuration without opening state or serving")
	keygen := flag.String("generate-key", "", "create protected issuer key at path; never overwrite")
	issuer := flag.String("issuer", "", "HTTPS portal origin for key generation")
	keyID := flag.String("key-id", "", "issuer key identity")
	trust := flag.String("trust-output", "", "write regional public trust file; never overwrite")
	flag.Parse()
	if *keygen != "" {
		return generateKey(*keygen, *trust, *issuer, *keyID)
	}
	cfg, err := portal.LoadConfig(*config)
	if err != nil {
		return err
	}
	if *validate {
		fmt.Println("configuration valid (offline)")
		return nil
	}
	store, err := portal.OpenStore(cfg.StatePath)
	if err != nil {
		return err
	}
	defer store.Close()
	app := &portal.Server{Store: store, Origin: cfg.PublicOrigin, Regions: map[string]*portal.RegionalClient{}}
	for _, region := range cfg.Regions {
		client, err := portal.NewRegionalClient(region, cfg.SignerFile)
		if err != nil {
			return err
		}
		app.Regions[region.PoolID] = client
	}
	handler, err := app.Handler()
	if err != nil {
		return err
	}
	server := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if err := store.Prune(now); err != nil {
					log.Print("portal authentication cleanup failed")
				}
			}
		}
	}()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
func generateKey(path, trustPath, issuer, id string) error {
	if issuer == "" || id == "" || trustPath == "" || path == trustPath {
		return fmt.Errorf("issuer, key-id and separate trust-output required")
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	file := memberauth.PrivateKeyFile{Issuer: issuer, KeyID: id, PrivateKey: base64.StdEncoding.EncodeToString(private)}
	now := time.Now().UTC()
	trust := memberauth.Trust{Issuer: issuer, Keys: []memberauth.PublicKey{{ID: id, PublicKey: base64.StdEncoding.EncodeToString(public), NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(1, 0, 0)}}}
	// Create both exclusively before writing either: existing keys are never replaced.
	keyFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer keyFile.Close()
	trustFile, err := os.OpenFile(trustPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		os.Remove(path)
		return err
	}
	defer trustFile.Close()
	if err := json.NewEncoder(keyFile).Encode(file); err != nil {
		return err
	}
	if err := keyFile.Sync(); err != nil {
		return err
	}
	if err := json.NewEncoder(trustFile).Encode(trust); err != nil {
		return err
	}
	return trustFile.Sync()
}
