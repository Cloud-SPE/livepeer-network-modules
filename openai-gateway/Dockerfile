# syntax=docker/dockerfile:1
# Reference OpenAI-compatible gateway image.
# Image: tztcloud/livepeer-openai-gateway-reference:<tag>
#
# Build context is the monorepo root because the gateway depends on
# the customer-portal/ and gateway-adapters/ts/ workspace packages,
# and dynamically loads .proto files from livepeer-network-protocol/proto/
# at runtime (via @grpc/proto-loader).

ARG NODE_VERSION=22

FROM node:${NODE_VERSION}-alpine AS deps

WORKDIR /src
RUN npm install -g pnpm@9.0.0
COPY pnpm-workspace.yaml pnpm-lock.yaml package.json ./
COPY customer-portal/package.json ./customer-portal/
COPY customer-portal/frontend/shared/package.json ./customer-portal/frontend/shared/
COPY gateway-adapters/ts/package.json ./gateway-adapters/ts/
COPY openai-gateway/package.json ./openai-gateway/
RUN pnpm install --frozen-lockfile --ignore-scripts

FROM node:${NODE_VERSION}-alpine AS builder

WORKDIR /src
RUN npm install -g pnpm@9.0.0
COPY --from=deps /src/node_modules ./node_modules
COPY --from=deps /src/customer-portal/node_modules ./customer-portal/node_modules
COPY --from=deps /src/customer-portal/frontend/shared/node_modules ./customer-portal/frontend/shared/node_modules
COPY --from=deps /src/gateway-adapters/ts/node_modules ./gateway-adapters/ts/node_modules
COPY --from=deps /src/openai-gateway/node_modules ./openai-gateway/node_modules
COPY pnpm-workspace.yaml pnpm-lock.yaml package.json ./
COPY customer-portal ./customer-portal
COPY gateway-adapters/ts ./gateway-adapters/ts
COPY openai-gateway ./openai-gateway
RUN pnpm -F @livepeer-rewrite/customer-portal-shared build \
 && pnpm -F @livepeer-rewrite/customer-portal build \
 && pnpm -F @tztcloud/livepeer-gateway-middleware build \
 && pnpm -F @livepeer-rewrite/openai-gateway build \
 && pnpm --filter @livepeer-rewrite/openai-gateway --prod deploy /out/openai-gateway

FROM gcr.io/distroless/nodejs22-debian12:nonroot AS runtime

WORKDIR /app
COPY --from=builder --chown=65532:65532 /out/openai-gateway/ /app/
COPY --chown=65532:65532 customer-portal/migrations/ /app/customer-portal-migrations/

# Proto files for @grpc/proto-loader's runtime parsing.
COPY --chown=65532:65532 livepeer-network-protocol/proto/ /app/proto/
COPY --chown=65532:65532 proto-contracts/ /app/proto-contracts/

EXPOSE 3000
CMD ["/app/dist/index.js"]
