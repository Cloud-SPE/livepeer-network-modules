# syntax=docker/dockerfile:1
# vtuber-gateway runtime image. Build context = monorepo root.
#
# Mirrors `openai-gateway/Dockerfile` shape (per plan 0013-openai
# parity). Per core belief #16, NODE_VERSION ARG bumps to latest stable
# on each release.

ARG NODE_VERSION=22

FROM node:${NODE_VERSION}-alpine AS deps

WORKDIR /src
COPY vtuber-gateway/package.json vtuber-gateway/package-lock.json* ./
RUN npm install --no-audit --no-fund

FROM node:${NODE_VERSION}-alpine AS builder

WORKDIR /src
COPY --from=deps /src/node_modules ./node_modules
COPY vtuber-gateway/package.json vtuber-gateway/tsconfig.json ./
COPY vtuber-gateway/src/ ./src/
RUN npx tsc

FROM node:${NODE_VERSION}-alpine AS runtime

WORKDIR /app
COPY --from=builder /src/dist /app/dist
COPY --from=builder /src/node_modules /app/node_modules
COPY vtuber-gateway/package.json ./
COPY vtuber-gateway/migrations/ /app/migrations/
COPY livepeer-network-protocol/proto/ /app/proto/

EXPOSE 3001
ENTRYPOINT ["node", "/app/dist/index.js"]
