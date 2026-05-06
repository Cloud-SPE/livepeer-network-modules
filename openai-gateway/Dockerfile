# syntax=docker/dockerfile:1
# Reference OpenAI-compatible gateway image.
# Image: tztcloud/livepeer-openai-gateway-reference:<tag>
#
# Build context is the monorepo root because the gateway dynamically
# loads .proto files from livepeer-network-protocol/proto/ at runtime
# (via @grpc/proto-loader). The .proto files are copied into the runtime
# image at /app/proto/.
#
# Per core belief #16, NODE_VERSION ARG is bumped to latest stable on
# each release.

ARG NODE_VERSION=22

FROM node:${NODE_VERSION}-alpine AS deps

WORKDIR /src
COPY openai-gateway/package.json openai-gateway/package-lock.json* ./
RUN npm install --no-audit --no-fund

FROM node:${NODE_VERSION}-alpine AS builder

WORKDIR /src
COPY --from=deps /src/node_modules ./node_modules
COPY openai-gateway/package.json openai-gateway/tsconfig.json ./
COPY openai-gateway/src/ ./src/
RUN npx tsc

FROM node:${NODE_VERSION}-alpine AS runtime

WORKDIR /app
COPY --from=builder /src/dist /app/dist
COPY --from=builder /src/node_modules /app/node_modules
COPY openai-gateway/package.json ./

# Proto files for @grpc/proto-loader's runtime parsing.
COPY livepeer-network-protocol/proto/ /app/proto/

EXPOSE 3000
ENTRYPOINT ["node", "/app/dist/index.js"]
