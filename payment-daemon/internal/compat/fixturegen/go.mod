// Separate Go module so importing go-livepeer (libp2p, ipfs-core, ffmpeg
// cgo bindings) does not pollute the daemon binary's dependency graph.
// The daemon binary's own module never resolves this go.mod.
//
// Q3-pinned: go-livepeer pinned at v0.8.10 (latest stable tag at
// plan-0016 implementation time). Bumping the pin requires regenerating
// the fixture and asserting bytes-equal across the bump.
module github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/compat/fixturegen

go 1.25.0

require (
	github.com/livepeer/go-livepeer v0.8.10
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/golang/mock v1.6.0 // indirect
	golang.org/x/net v0.34.0 // indirect
	golang.org/x/sys v0.29.0 // indirect
	golang.org/x/text v0.21.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20241209162323-e6fa225c2576 // indirect
	google.golang.org/grpc v1.68.1 // indirect
)
