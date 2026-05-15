# infra — shared services, image build script, and staged scenario stacks

Operator helpers that don't belong inside any single component.

## Layout

```
infra/
├── scenarios/
│   ├── orchestrator-onboarding/   # active orchestrator onboarding guide + stacks
│   │   ├── README.md              # the onboarding guide
│   │   ├── secure-orch-control-plane/
│   │   ├── orch-coordinator/
│   │   ├── capability-broker/
│   │   ├── ingress-traefik/
│   │   ├── ingress-cloudflared/
│   │   └── ingress-nginx/
│   ├── gateway-onboarding/        # active gateway onboarding guide + stacks
│   │   ├── README.md              # the gateway onboarding guide
│   │   ├── openai-gateway/
│   │   ├── video-gateway/
│   │   ├── vtuber-gateway/        # PREVIEW
│   │   ├── ingress-traefik/
│   │   └── ingress-nginx/
│   └── archive/                   # earlier scenarios, preserved for reference
├── compose/
│   ├── docker-compose.yml         # shared services (postgres, redis, rustfs) — profile-gated
│   └── .env.example               # copy to .env, edit, then --env-file in compose
└── scripts/
    └── build-images.sh            # builds every image in dependency order
```

## Building images

The script pulls **no** images — it builds everything from source in
dependency order. Tier 0 (`codecs-builder`, `python-runner-base`,
`python-gpu-runner-base`, `python-gpu-media-runner-base`) lands first so
the multi-arch video runners, CPU Python tooling, and GPU Python runners
can `FROM` them.

```sh
# Build everything as tztcloud/<name>:v1.1.0
./infra/scripts/build-images.sh

# Build a single component (substring match)
./infra/scripts/build-images.sh capability-broker

# Custom registry / tag
REGISTRY=ghcr.io/myorg TAG=2026.5.7 ./infra/scripts/build-images.sh

# Build then push
PUSH=1 REGISTRY=ghcr.io/myorg TAG=2026.5.7 ./infra/scripts/build-images.sh
```

Defaults: `REGISTRY=tztcloud`, `TAG=v1.1.0`, `PUSH=0`.

## Scenario stacks

`infra/scenarios/` is organized by audience:

- **`orchestrator-onboarding/`** — the active orchestrator onboarding
  guide and every stack referenced by it (Secure Orch, Orch Coordinator,
  Capability Broker, three ingress options). The `README.md` at that path
  is the guide itself.
- **`gateway-onboarding/`** — the active gateway onboarding guide and
  every stack referenced by it (OpenAI / Video / Vtuber gateways, plus
  Traefik and Nginx ingress).
- **`archive/`** — earlier multi-module scenarios kept for historical
  reference. Not maintained against the current onboarding flow.

Each scenario directory inside contains:

- `docker-compose.yml`
- optional overlays (`docker-compose.<ingress>.yml`)
- `.env.example`
- any scenario-local config files
- a `README.md`

## Shared services

`infra/compose/docker-compose.yml` runs Postgres / Redis / RustFS behind
profiles. Per-component compose files (e.g. `video-gateway/compose/`)
expect these reachable via `${DATABASE_URL}` / `${REDIS_URL}` env vars
and do **not** include them inline.

```sh
# Postgres only
docker compose -f infra/compose/docker-compose.yml --profile pg up -d

# Postgres + Redis + RustFS (full stack)
docker compose -f infra/compose/docker-compose.yml \
  --profile pg --profile redis --profile rustfs up -d
```

The `rustfs` profile also brings up a one-shot `rustfs-init` container
that creates the default bucket (`transcoded`) via the S3 API.

Tear down with `down -v` to delete the volumes; without `-v` the data
volumes (`pg-data`, `redis-data`, `rustfs-data`) persist.

## Per-component compose files

Each deployable module ships its own `<component>/compose/docker-compose.yml`
that **runs** the prebuilt image — no `build:` blocks. The matching
`<component>/compose/.env.example` captures the module-local knobs.

That keeps the build path (`infra/scripts/build-images.sh`), the
module-local run path (`<component>/compose/`), and the staged topology
examples (`infra/scenarios/`) cleanly separated.
