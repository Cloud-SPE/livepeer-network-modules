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
RUN pnpm -F @livepeer-rewrite/customer-portal build \
 && pnpm -F @livepeer-rewrite/vtuber-gateway build

FROM node:${NODE_VERSION}-alpine AS runtime

WORKDIR /app
COPY --from=builder /src/vtuber-gateway/dist /app/dist
COPY --from=builder /src/vtuber-gateway/package.json /app/
COPY --from=builder /src/vtuber-gateway/migrations /app/migrations
COPY --from=builder /src/customer-portal/dist /app/node_modules/@livepeer-rewrite/customer-portal/dist
COPY --from=builder /src/customer-portal/package.json /app/node_modules/@livepeer-rewrite/customer-portal/
COPY --from=builder /src/customer-portal/migrations /app/node_modules/@livepeer-rewrite/customer-portal/migrations
COPY --from=builder /src/node_modules /app/node_modules
COPY livepeer-network-protocol/proto/ /app/proto/

EXPOSE 3001
ENTRYPOINT ["node", "/app/dist/index.js"]
