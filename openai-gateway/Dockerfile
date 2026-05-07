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
COPY gateway-adapters/ts/package.json ./gateway-adapters/ts/
COPY openai-gateway/package.json ./openai-gateway/
RUN pnpm install --frozen-lockfile --ignore-scripts

FROM node:${NODE_VERSION}-alpine AS builder

WORKDIR /src
RUN npm install -g pnpm@9.0.0
COPY --from=deps /src/node_modules ./node_modules
COPY --from=deps /src/customer-portal/node_modules ./customer-portal/node_modules
COPY --from=deps /src/gateway-adapters/ts/node_modules ./gateway-adapters/ts/node_modules
COPY --from=deps /src/openai-gateway/node_modules ./openai-gateway/node_modules
COPY pnpm-workspace.yaml pnpm-lock.yaml package.json ./
COPY customer-portal ./customer-portal
COPY gateway-adapters/ts ./gateway-adapters/ts
COPY openai-gateway ./openai-gateway
RUN pnpm -F @livepeer-rewrite/customer-portal build \
 && pnpm -F @tztcloud/livepeer-gateway-middleware build \
 && pnpm -F @livepeer-rewrite/openai-gateway build

FROM node:${NODE_VERSION}-alpine AS runtime

WORKDIR /app
COPY --from=builder /src/openai-gateway/dist /app/dist
COPY --from=builder /src/openai-gateway/package.json /app/
COPY --from=builder /src/customer-portal/dist /app/node_modules/@livepeer-rewrite/customer-portal/dist
COPY --from=builder /src/customer-portal/package.json /app/node_modules/@livepeer-rewrite/customer-portal/
COPY --from=builder /src/customer-portal/migrations /app/node_modules/@livepeer-rewrite/customer-portal/migrations
COPY --from=builder /src/gateway-adapters/ts/dist /app/node_modules/@tztcloud/livepeer-gateway-middleware/dist
COPY --from=builder /src/gateway-adapters/ts/package.json /app/node_modules/@tztcloud/livepeer-gateway-middleware/
COPY --from=builder /src/node_modules /app/node_modules

# Proto files for @grpc/proto-loader's runtime parsing.
COPY livepeer-network-protocol/proto/ /app/proto/

EXPOSE 3000
ENTRYPOINT ["node", "/app/dist/index.js"]
