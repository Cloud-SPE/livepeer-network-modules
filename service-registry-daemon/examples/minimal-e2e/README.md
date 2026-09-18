# In-process signed discovery example

This example generates a throwaway key, constructs a protocol envelope in the
example itself, signs it, and seeds an in-memory chain and static fetcher. It
then resolves broker nodes and calls the Go-native Select API. It does not use
publisher build/sign RPCs; those RPCs no longer exist.

From the component directory with the Go development toolchain:

```sh
go run ./examples/minimal-e2e
```

The example prints signed-manifest, resolved-node and selected-route summaries.
Addresses, signatures, byte counts and synthetic node IDs are not fixed output
contracts. It demonstrates opaque capability matching and signature recovery.
The static fetcher avoids HTTP hosting, and no live-health provider is wired.
It does not submit work, validate payment funding, or redeem on-chain tickets.

For production, use the coordinator/cold-console signing cycle and the daemon
Docker image. See [running the daemon](../../docs/operations/running-the-daemon.md).
