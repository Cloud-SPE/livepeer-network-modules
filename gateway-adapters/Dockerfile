# syntax=docker/dockerfile:1
#
# gateway-adapters dev image. Used for `make test` (compile + run unit
# tests). The package ships as npm; this Dockerfile is the test/build
# environment per core belief #15.
#
# Per core belief #16, NODE_VERSION is bumped to latest stable on each
# release. Renovate / dependabot tracks this.

ARG NODE_VERSION=22

FROM node:${NODE_VERSION}-alpine AS deps

WORKDIR /src
COPY package.json package-lock.json* ./
RUN npm install --no-audit --no-fund

FROM node:${NODE_VERSION}-alpine AS test

WORKDIR /src
COPY --from=deps /src/node_modules ./node_modules
COPY package.json tsconfig.json ./
COPY src/ ./src/
COPY test/ ./test/

# Type-check + compile + run unit tests as part of the image build so
# `make test` is just `docker build`.
RUN npx tsc
# sh -c so the glob expands. Node's --test takes a list of files; passing a
# bare directory makes it try to load the directory as a single module.
ENTRYPOINT ["sh", "-c", "node --test dist/test/*.test.js"]
