# infra — shared compose services + image build script

Operator helpers that don't belong inside any single component.

## Layout

```
infra/
├── compose/
│   ├── docker-compose.yml     # shared services (postgres, redis, minio) — profile-gated
│   └── .env.example           # copy to .env, edit, then --env-file in compose
└── scripts/
    └── build-images.sh        # builds every image in dependency order
```

## Building images

The script pulls **no** images — it builds everything from source in
dependency order. Tier 0 (`codecs-builder`, `python-runner-base`) lands
first so the multi-arch video runners and Python ML runners can FROM
them.

```sh
# Build everything as tztcloud/<name>:v1.0.0
./infra/scripts/build-images.sh

# Build a single component (substring match)
./infra/scripts/build-images.sh capability-broker

# Custom registry / tag
REGISTRY=ghcr.io/myorg TAG=2026.5.7 ./infra/scripts/build-images.sh

# Build then push
PUSH=1 REGISTRY=ghcr.io/myorg TAG=2026.5.7 ./infra/scripts/build-images.sh
```

Defaults: `REGISTRY=tztcloud`, `TAG=v1.0.0`, `PUSH=0`.

## Shared services

`infra/compose/docker-compose.yml` runs Postgres / Redis / MinIO behind
profiles. Per-component compose files (e.g. `video-gateway/compose/`)
expect these reachable via `${DATABASE_URL}` / `${REDIS_URL}` env vars
and do **not** include them inline.

```sh
# Postgres only
docker compose -f infra/compose/docker-compose.yml --profile pg up -d

# Postgres + Redis + MinIO (full stack)
docker compose -f infra/compose/docker-compose.yml \
  --profile pg --profile redis --profile minio up -d
```

Tear down with `down -v` to delete the volumes; without `-v` the data
volumes (`pg-data`, `redis-data`, `minio-data`) persist.

## Per-component compose files

Each deployable module ships its own `<component>/compose/docker-compose.yml`
that **runs** the prebuilt image — no `build:` blocks. That keeps the
build path (`infra/scripts/build-images.sh`) and the run path (per-module
compose) cleanly separated.
