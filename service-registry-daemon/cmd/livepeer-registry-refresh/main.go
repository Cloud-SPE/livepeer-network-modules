// livepeer-registry-refresh is the operator-facing CLI for
// regenerating + signing the service-registry manifest. It reads an
// unsigned raw-registry-manifest.json proposal, connects to a running
// publisher daemon over its unix socket, and invokes
// Publisher.BuildAndSign in one round-trip. The signed
// registry-manifest.json is written to stdout (or to --out).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	registryv1 "github.com/Cloud-SPE/livepeer-network-rewrite/proto-contracts/livepeer/registry/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("livepeer-registry-refresh", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socket := fs.String("socket", "/var/run/livepeer/service-registry.sock", "publisher daemon's unix socket")
	rawManifestPath := fs.String("raw-manifest", "", "path to raw-registry-manifest.json (required)")
	outPath := fs.String("out", "", "where to write the signed registry-manifest.json (default: stdout)")
	timeout := fs.Duration("timeout", 30*time.Second, "overall RPC timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *rawManifestPath == "" {
		fmt.Fprintln(stderr, "livepeer-registry-refresh: --raw-manifest is required")
		fs.Usage()
		return 2
	}

	body, err := loadRawManifest(*rawManifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "livepeer-registry-refresh: load raw manifest: %v\n", err)
		return 1
	}

	conn, err := grpc.NewClient("unix://"+*socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(stderr, "livepeer-registry-refresh: dial %s: %v\n", *socket, err)
		return 1
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	cli := registryv1.NewPublisherClient(conn)
	signed, err := cli.BuildAndSign(ctx, &registryv1.BuildAndSignRequest{
		ManifestJson: body,
	})
	if err != nil {
		fmt.Fprintf(stderr, "livepeer-registry-refresh: BuildAndSign: %v\n", err)
		return 1
	}

	signedBody := signed.GetManifestJson()
	if *outPath != "" {
		if err := os.WriteFile(*outPath, signedBody, 0o644); err != nil {
			fmt.Fprintf(stderr, "livepeer-registry-refresh: write %s: %v\n", *outPath, err)
			return 1
		}
		fmt.Fprintf(stderr, "livepeer-registry-refresh: wrote %s (%d bytes, sig=%s)\n",
			*outPath, len(signedBody), signed.GetSignatureValue())
		return 0
	}
	if _, err := stdout.Write(signedBody); err != nil {
		fmt.Fprintf(stderr, "livepeer-registry-refresh: stdout: %v\n", err)
		return 1
	}
	if len(signedBody) > 0 && signedBody[len(signedBody)-1] != '\n' {
		fmt.Fprintln(stdout)
	}
	return 0
}

func loadRawManifest(path string) ([]byte, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("json: trailing data after manifest object")
	}
	return body, nil
}
