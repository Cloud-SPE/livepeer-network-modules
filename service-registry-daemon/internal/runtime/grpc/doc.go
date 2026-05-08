// Package grpc hosts the runtime layer of the registry daemon.
//
// In v1 of the scaffold, this package exposes a Go-native handler
// surface (the Server type) that mirrors the gRPC RPCs declared in
// proto/livepeer/registry/v1/. The intent is:
//
//   - The .proto files in proto/livepeer/registry/v1/ are the
//     source-of-truth contract.
//   - `make proto` generates Go bindings under proto/gen/...
//   - A thin grpc.RegisterResolverServer adapter (added in a follow-up
//     exec-plan) binds the generated server interface to the Go-native
//     handlers in this package.
//
// This separation lets the scaffold compile and pass tests without buf
// installed: developers run `make proto` only when they touch the
// schema. The Go-native Server is also useful in-process for
// integration tests and the minimal-e2e example.
package grpc
