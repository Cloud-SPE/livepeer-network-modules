package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/brokerpush"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"gopkg.in/yaml.v3"
)

func runInitializeIdentity(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("init-identity", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	data := fs.String("data-dir", "", "persistent controller data directory")
	expected := fs.String("expect-pool-id", "", "require an existing store with this identity; never initialize a replacement")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *data == "" || fs.NArg() != 0 {
		return errors.New("--data-dir is required")
	}
	if *expected != "" {
		if _, err := os.Stat(filepath.Join(*data, "pool-controller.db")); err != nil {
			return fmt.Errorf("expected pool requires its existing pool-controller.db: %w", err)
		}
	}
	store, err := repo.Open(*data)
	if err != nil {
		return err
	}
	defer store.Close()
	if *expected != "" && store.PoolID() != *expected {
		return errors.New("controller pool identity mismatch")
	}
	return json.NewEncoder(out).Encode(map[string]string{"pool_id": store.PoolID()})
}
func runValidateConfig(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("validate-config", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("config", "", "configuration path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" || fs.NArg() != 0 {
		return errors.New("--config is required")
	}
	file, err := os.Open(*path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	var typed config.Config
	if err := decoder.Decode(&typed); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("multiple config documents forbidden")
	}
	cfg, err := config.LoadFile(*path)
	if err != nil {
		return err
	}
	catalog, err := templates.Load(cfg.TemplateCatalogDir)
	if err != nil {
		return err
	}
	for _, target := range cfg.Bootstrap.BrokerTargets() {
		if _, err := brokerpush.FilterOffers(catalog.All(), nil, target.TemplateIDs); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintln(out, "configuration valid (offline)")
	return err
}
