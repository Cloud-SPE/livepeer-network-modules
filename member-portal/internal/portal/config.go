package portal

import (
	"bytes"
	"errors"
	"io"
	"os"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/memberauth"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen       string   `yaml:"listen"`
	PublicOrigin string   `yaml:"public_origin"`
	StatePath    string   `yaml:"state_path"`
	SignerFile   string   `yaml:"signer_file"`
	Regions      []Region `yaml:"regions"`
}

func LoadConfig(path string) (Config, error) {
	var cfg Config
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	d := yaml.NewDecoder(bytes.NewReader(raw))
	d.KnownFields(true)
	if err := d.Decode(&cfg); err != nil {
		return cfg, err
	}
	if d.Decode(new(any)) != io.EOF {
		return cfg, errors.New("multiple config documents forbidden")
	}
	if cfg.Listen == "" || cfg.StatePath == "" || len(cfg.Regions) == 0 {
		return cfg, errors.New("listen, persistent state and regions required")
	}
	if _, err := parseOrigin(cfg.PublicOrigin); err != nil {
		return cfg, err
	}
	signer, err := memberauth.LoadSigner(cfg.SignerFile)
	if err != nil {
		return cfg, err
	}
	if signer.Issuer != cfg.PublicOrigin {
		return cfg, errors.New("member issuer must equal portal public origin")
	}
	seen := map[string]bool{}
	for _, region := range cfg.Regions {
		if seen[region.PoolID] {
			return cfg, errors.New("duplicate regional pool identity")
		}
		seen[region.PoolID] = true
		if _, err := NewRegionalClient(region, cfg.SignerFile); err != nil {
			return cfg, err
		}
	}
	return cfg, nil
}
