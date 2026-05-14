# syntax=docker/dockerfile:1
# vtuber-gateway runtime image. Build context = monorepo root.
#
# Depends on the customer-portal/ workspace package; mirrors the pnpm
# pattern used by video-gateway. Per core belief #16, NODE_VERSION ARG
# bumps to latest stable on each release.

ARG NODE_VERSION=22

FROM node:${NODE_VERSION}-alpine AS deps

WORKDIR /src
RUN npm install -g pnpm@9.0.0
COPY pnpm-workspace.yaml pnpm-lock.yaml package.json ./
COPY customer-portal/package.json ./customer-portal/
COPY vtuber-gateway/package.json ./vtuber-gateway/
RUN pnpm install --frozen-lockfile --ignore-scripts

FROM node:${NODE_VERSION}-alpine AS builder

WORKDIR /src
RUN npm install -g pnpm@9.0.0
COPY --from=deps /src/node_modules ./node_modules
COPY --from=deps /src/customer-portal/node_modules ./customer-portal/node_modules
COPY --from=deps /src/vtuber-gateway/node_modules ./vtuber-gateway/node_modules
COPY pnpm-workspace.yaml pnpm-lock.yaml package.json ./
COPY customer-portal ./customer-portal
COPY vtuber-gateway ./vtuber-gateway
RUN pnpm -F @livepeer-network-modules/customer-portal build \
 && pnpm -F @livepeer-network-modules/vtuber-gateway build \
 && pnpm --filter @livepeer-network-modules/vtuber-gateway --prod deploy /out/vtuber-gateway

FROM gcr.io/distroless/nodejs22-debian12:nonroot AS runtime

WORKDIR /app
COPY --from=builder --chown=65532:65532 /out/vtuber-gateway/ /app/
COPY --chown=65532:65532 livepeer-network-protocol/proto/ /app/proto/

EXPOSE 3001
CMD ["/app/dist/index.js"]
