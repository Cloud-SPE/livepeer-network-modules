# Multi-stage build for the daydream-gateway TS server.
# Stage 1: install deps + compile.
# Stage 2: minimal runtime with only the compiled JS + production deps.

FROM node:20-alpine AS build
WORKDIR /repo

# pnpm via corepack.
RUN corepack enable

# Copy workspace manifests so pnpm can resolve workspace deps
# (gateway-adapters/ts is a workspace dependency).
COPY pnpm-workspace.yaml package.json pnpm-lock.yaml ./
COPY gateway-adapters/ts/package.json gateway-adapters/ts/
COPY daydream-gateway/package.json daydream-gateway/

RUN pnpm install --frozen-lockfile

# Copy source and build.
COPY gateway-adapters/ts/ gateway-adapters/ts/
COPY daydream-gateway/ daydream-gateway/
COPY livepeer-network-protocol/proto/ livepeer-network-protocol/proto/

RUN pnpm -F @tztcloud/livepeer-gateway-middleware build
RUN pnpm -F @livepeer-rewrite/daydream-gateway build

# Stage 2: runtime.
FROM node:20-alpine AS runtime
WORKDIR /app
RUN corepack enable

ENV NODE_ENV=production

COPY --from=build /repo/pnpm-workspace.yaml /repo/package.json /repo/pnpm-lock.yaml ./
COPY --from=build /repo/daydream-gateway/package.json daydream-gateway/
COPY --from=build /repo/daydream-gateway/dist daydream-gateway/dist
COPY --from=build /repo/gateway-adapters/ts gateway-adapters/ts
COPY --from=build /repo/livepeer-network-protocol/proto livepeer-network-protocol/proto

RUN pnpm install --frozen-lockfile --prod

WORKDIR /app/daydream-gateway

EXPOSE 9100

HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD wget -qO- http://localhost:9100/healthz || exit 1

CMD ["node", "./dist/index.js"]
