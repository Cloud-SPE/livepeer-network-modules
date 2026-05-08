// smoke-client connects to a running daemon over a unix socket and
// exercises the Health and Resolver RPCs. Used by hand for sanity
// checks; not a unit test.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	registryv1 "github.com/Cloud-SPE/livepeer-network-rewrite/proto-contracts/livepeer/registry/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func main() {
	sock := "/tmp/reg.sock"
	if len(os.Args) > 1 {
		sock = os.Args[1]
	}
	conn, err := grpc.NewClient("unix://"+sock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial:", err)
		os.Exit(1)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	hcli := healthpb.NewHealthClient(conn)
	hres, err := hcli.Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "Health.Check:", err)
		os.Exit(1)
	}
	fmt.Println("Health:", hres.GetStatus())

	rcli := registryv1.NewResolverClient(conn)
	out, err := rcli.Health(ctx, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Resolver.Health:", err)
		os.Exit(1)
	}
	fmt.Printf("Resolver.Health: mode=%s chain_ok=%v cache=%d\n", out.GetMode(), out.GetChainOk(), out.GetCacheSize())

	// NotFound is expected — the resolver has nothing seeded.
	_, err = rcli.ResolveByAddress(ctx, &registryv1.ResolveByAddressRequest{
		EthAddress: "0xabcdef0000000000000000000000000000000000",
	})
	fmt.Println("ResolveByAddress (expect not_found):", err)
}
