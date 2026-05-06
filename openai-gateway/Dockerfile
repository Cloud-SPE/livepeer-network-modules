# syntax=docker/dockerfile:1
# Reference OpenAI-compatible gateway image.
# Image: tztcloud/livepeer-openai-gateway-reference:<tag>
# Per core belief #16, NODE_VERSION ARG is bumped to latest stable on each release.

ARG NODE_VERSION=22

FROM node:${NODE_VERSION}-alpine AS deps

WORKDIR /src
COPY package.json package-lock.json* ./
RUN npm install --no-audit --no-fund

FROM node:${NODE_VERSION}-alpine AS builder

WORKDIR /src
COPY --from=deps /src/node_modules ./node_modules
COPY package.json tsconfig.json ./
COPY src/ ./src/
RUN npx tsc

FROM node:${NODE_VERSION}-alpine AS runtime

WORKDIR /app
COPY --from=builder /src/dist /app/dist
COPY --from=builder /src/node_modules /app/node_modules
COPY package.json ./

EXPOSE 3000
ENTRYPOINT ["node", "/app/dist/index.js"]
