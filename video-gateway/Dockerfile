# syntax=docker/dockerfile:1
# Reference video-gateway image.
# Image: tztcloud/livepeer-video-gateway:<tag>
#
# Build context is the monorepo root because the gateway depends on the
# customer-portal/ workspace package.

ARG NODE_VERSION=22

FROM node:${NODE_VERSION}-alpine AS deps

WORKDIR /src
RUN npm install -g pnpm@9.0.0
COPY pnpm-workspace.yaml pnpm-lock.yaml package.json ./
COPY customer-portal/package.json ./customer-portal/
COPY customer-portal/frontend/shared/package.json ./customer-portal/frontend/shared/
COPY gateway-route-health/package.json ./gateway-route-health/
COPY video-gateway/package.json ./video-gateway/
RUN pnpm install --frozen-lockfile --ignore-scripts

FROM node:${NODE_VERSION}-alpine AS builder

WORKDIR /src
RUN npm install -g pnpm@9.0.0
COPY --from=deps /src/node_modules ./node_modules
COPY --from=deps /src/customer-portal/node_modules ./customer-portal/node_modules
COPY --from=deps /src/customer-portal/frontend/shared/node_modules ./customer-portal/frontend/shared/node_modules
COPY --from=deps /src/gateway-route-health/node_modules ./gateway-route-health/node_modules
COPY --from=deps /src/video-gateway/node_modules ./video-gateway/node_modules
COPY pnpm-workspace.yaml pnpm-lock.yaml package.json ./
COPY customer-portal ./customer-portal
COPY gateway-route-health ./gateway-route-health
COPY video-gateway ./video-gateway
RUN pnpm -F @livepeer-network-modules/customer-portal build \
 && pnpm -F @livepeer-network-modules/gateway-route-health build \
 && pnpm -F @livepeer-network-modules/video-gateway build \
 && pnpm --filter @livepeer-network-modules/video-gateway --prod deploy /out/video-gateway

FROM gcr.io/distroless/nodejs22-debian12:nonroot AS runtime

WORKDIR /app
COPY --from=builder --chown=65532:65532 /out/video-gateway/ /app/
COPY --chown=65532:65532 customer-portal/migrations/ /app/customer-portal-migrations/
COPY --chown=65532:65532 proto-contracts/ /app/proto-contracts/

EXPOSE 3000 1935
CMD ["/app/dist/index.js"]
