module github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon

go 1.25.7

// Source repo (livepeer-service-registry) targeted Go 1.25 with a pinned
// toolchain. This monorepo now standardizes on Go 1.25.7 across the daemon
// modules so local, CI, and release builds stay aligned.

require (
	github.com/ethereum/go-ethereum v1.17.2
	github.com/prometheus/client_golang v1.23.2
	go.etcd.io/bbolt v1.4.3
	google.golang.org/grpc v1.80.0
	google.golang.org/protobuf v1.36.11
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	go.mongodb.org/mongo-driver v1.17.9 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
)

require (
	github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons v0.0.0-20260430170820-93fdaeb71d5e
	github.com/Cloud-SPE/livepeer-network-rewrite/proto-contracts v0.0.0
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/ProjectZKM/Ziren/crates/go-runtime/zkvm_runtime v0.0.0-20260416073033-7c2071eaa8d4 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bits-and-blooms/bitset v1.24.4 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/consensys/gnark-crypto v0.20.1 // indirect
	github.com/crate-crypto/go-eth-kzg v1.5.0 // indirect
	github.com/deckarep/golang-set/v2 v2.9.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.4.1 // indirect
	github.com/ethereum/c-kzg-4844/v2 v2.1.7 // indirect
	github.com/fsnotify/fsnotify v1.10.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/holiman/uint256 v1.3.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.67.5 // indirect
	github.com/prometheus/procfs v0.20.1 // indirect
	github.com/shirou/gopsutil v3.21.11+incompatible // indirect
	github.com/supranational/blst v0.3.16 // indirect
	github.com/tklauser/go-sysconf v0.3.16 // indirect
	github.com/tklauser/numcpus v0.11.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.43.0 // indirect
	go.opentelemetry.io/otel/metric v1.43.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.40.0 // indirect
	go.opentelemetry.io/otel/trace v1.43.0 // indirect
	golang.org/x/crypto v0.50.0 // indirect
	golang.org/x/net v0.53.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260427160629-7cedc36a6bc4 // indirect
)

// chain-commons is a sibling module in this monorepo, not yet published to
// a remote. Use a workspace-style replace until plan 0008 sets up tagged
// versioning. CI passes by virtue of the local checkout being adjacent.
replace github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons => ../chain-commons

replace github.com/Cloud-SPE/livepeer-network-rewrite/proto-contracts => ../proto-contracts
