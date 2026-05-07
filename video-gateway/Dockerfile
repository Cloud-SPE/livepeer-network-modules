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
COPY video-gateway/package.json ./video-gateway/
RUN pnpm install --frozen-lockfile --ignore-scripts

FROM node:${NODE_VERSION}-alpine AS builder

WORKDIR /src
RUN npm install -g pnpm@9.0.0
COPY --from=deps /src/node_modules ./node_modules
COPY --from=deps /src/customer-portal/node_modules ./customer-portal/node_modules
COPY --from=deps /src/video-gateway/node_modules ./video-gateway/node_modules
COPY pnpm-workspace.yaml pnpm-lock.yaml package.json ./
COPY customer-portal ./customer-portal
COPY video-gateway ./video-gateway
RUN pnpm -F @livepeer-rewrite/customer-portal build && pnpm -F @livepeer-rewrite/video-gateway build

FROM node:${NODE_VERSION}-alpine AS runtime

WORKDIR /app
COPY --from=builder /src/video-gateway/dist /app/dist
COPY --from=builder /src/video-gateway/package.json /app/
COPY --from=builder /src/video-gateway/migrations /app/migrations
COPY --from=builder /src/customer-portal/dist /app/node_modules/@livepeer-rewrite/customer-portal/dist
COPY --from=builder /src/customer-portal/package.json /app/node_modules/@livepeer-rewrite/customer-portal/
COPY --from=builder /src/customer-portal/migrations /app/node_modules/@livepeer-rewrite/customer-portal/migrations
COPY --from=builder /src/node_modules /app/node_modules

EXPOSE 3000 1935
ENTRYPOINT ["node", "/app/dist/index.js"]
