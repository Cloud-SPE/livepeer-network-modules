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

FROM node:${NODE_VERSION}-alpine AS test

WORKDIR /src
COPY --from=deps /src/node_modules ./node_modules
COPY package.json tsconfig.json tsconfig.test.json ./
COPY src/ ./src/
COPY test/ ./test/

# Compile against tsconfig.test.json (rootDir=. + outDir=./dist-test) so
# both src/ and test/ end up under dist-test/. Run node --test with glob
# expansion via sh.
RUN npx tsc -p tsconfig.test.json
ENTRYPOINT ["sh", "-c", "node --test dist-test/test/*.test.js"]

FROM node:${NODE_VERSION}-alpine AS runtime

WORKDIR /app
COPY --from=builder /src/dist /app/dist
COPY --from=builder /src/node_modules /app/node_modules
COPY package.json ./

EXPOSE 3000
ENTRYPOINT ["node", "/app/dist/index.js"]
