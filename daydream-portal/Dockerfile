# syntax=docker/dockerfile:1
# daydream-portal runtime image. Build context = monorepo root.
# Mirrors the vtuber-gateway pattern: install workspace deps in a
# dedicated stage, build, then deploy a pruned subtree into a
# distroless runtime.

ARG NODE_VERSION=22

FROM node:${NODE_VERSION}-alpine AS deps

WORKDIR /src
RUN npm install -g pnpm@9.0.0
COPY pnpm-workspace.yaml pnpm-lock.yaml package.json ./
COPY customer-portal/package.json ./customer-portal/
COPY gateway-route-health/package.json ./gateway-route-health/
COPY daydream-portal/package.json ./daydream-portal/
RUN pnpm install --frozen-lockfile --ignore-scripts

FROM node:${NODE_VERSION}-alpine AS builder

WORKDIR /src
RUN npm install -g pnpm@9.0.0
COPY --from=deps /src/node_modules ./node_modules
COPY --from=deps /src/customer-portal/node_modules ./customer-portal/node_modules
COPY --from=deps /src/gateway-route-health/node_modules ./gateway-route-health/node_modules
COPY --from=deps /src/daydream-portal/node_modules ./daydream-portal/node_modules
COPY pnpm-workspace.yaml pnpm-lock.yaml package.json ./
COPY customer-portal ./customer-portal
COPY gateway-route-health ./gateway-route-health
COPY daydream-portal ./daydream-portal
RUN pnpm -F @livepeer-network-modules/customer-portal build \
 && pnpm -F @livepeer-network-modules/gateway-route-health build \
 && pnpm -F @livepeer-network-modules/daydream-portal build \
 && pnpm --filter @livepeer-network-modules/daydream-portal --prod deploy /out/daydream-portal

FROM gcr.io/distroless/nodejs22-debian12:nonroot AS runtime

WORKDIR /app
COPY --from=builder --chown=65532:65532 /out/daydream-portal/ /app/

EXPOSE 8080
CMD ["/app/dist/index.js"]
