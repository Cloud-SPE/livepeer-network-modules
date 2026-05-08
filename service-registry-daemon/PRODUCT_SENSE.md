# PRODUCT_SENSE — livepeer-service-registry

## What we're building

Infrastructure. A standalone daemon that lets any application on the Livepeer network publish or discover orchestrator capabilities — transcoding, AI inference, anything else operators choose to advertise — without depending on `go-livepeer` and without putting structured metadata on chain.

Not a product for end users; a product for engineers building products.

## Who uses this

Three personas.

### The orchestrator operator (publisher mode)

Someone who runs a Livepeer orchestrator and wants their capabilities to be discoverable by gateways and bridges. They care about:

- One place to declare what their nodes do — transcoding profiles, AI models, GPUs, geo, prices.
- Not having to issue a chain transaction for every metadata change.
- Their claims being trusted (signed by the same eth key the chain associates with them).
- The publish step not being mixed into a 100k-line media-processing binary.

### The consumer-app developer (resolver mode)

Someone building a gateway, bridge, or AI router that needs to find orchestrators that match a job. They care about:

- A single gRPC call (`Resolve(ethAddress)` for orch diagnostics, `Select(capability, offering, ...)` for gateway routing) returning structured discovery data without leaking orch-internal fields.
- Working with old orchestrators (legacy `serviceURI`) and new ones from a single API.
- Not having to dial Ethereum, parse opaque strings, or implement signature verification.
- Their static `nodes.yaml` allowlist still being authoritative — discovery augments, not replaces.

### The transcoding client (legacy mode)

Existing `go-livepeer` clients that just want a URL to dial. They care about:

- Nothing changing for them. The on-chain `serviceURI` continues to be a plain URL. The new format adds a sibling `/.well-known/...` endpoint, which transcoding clients can ignore.

## What "good" looks like

- A bridge developer can drop the resolver daemon next to their app, point it at chain RPC, and replace 200 lines of bespoke discovery code with a 10-line gRPC client.
- An orchestrator operator can run the publisher daemon for months, edit a YAML, and have new capabilities discoverable globally within minutes — no chain transaction unless the URL itself changed.
- An old transcoding client that only knows `getServiceURI(addr)` keeps working unchanged.
- The same daemon serves AI workloads, transcoding, and capability types invented next year. Adding a new workload type requires zero code changes in this repo.

## Anti-goals

- This is not a marketplace. It does not match jobs to orchestrators or take a fee. It returns lists; the consumer chooses.
- This is not a payment system. Use `payment-daemon` for ticket-based settlement; the registry only *advertises* prices.
- This is not a substitute for go-livepeer's gRPC capability advertisement. Gateways using go-livepeer's existing `GetOrchestrator` RPC keep working unchanged. The daemon is purely additive.
- This is not a CDN for capability data. The publisher writes a JSON file; the operator hosts it via their existing HTTP infrastructure. We do not run a public manifest mirror.
- This is not workload-specific. There is no AI service, no transcoding service, no openai service. There is one service: capability advertisement. The data inside is opaque to the daemon.
