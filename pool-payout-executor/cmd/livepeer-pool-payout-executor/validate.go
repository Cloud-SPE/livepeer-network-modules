package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/config"
	"io"
)

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
	if _, err := config.LoadFile(*path); err != nil {
		return err
	}
	_, err := fmt.Fprintln(out, "configuration valid (offline)")
	return err
}
