# syntax=docker/dockerfile:1
# customer-portal is a shared library, not a long-running service. This image
# exists as a slim distributable artifact for smoke/debug import checks.

ARG NODE_VERSION=22

FROM node:${NODE_VERSION}-alpine AS deps

WORKDIR /src
COPY package.json ./
RUN npm install --no-audit --no-fund

FROM node:${NODE_VERSION}-alpine AS builder

WORKDIR /src
COPY --from=deps /src/node_modules ./node_modules
COPY package.json tsconfig.json ./
COPY src ./src
COPY migrations ./migrations
COPY README.md DESIGN.md ./
RUN npm run build

FROM node:${NODE_VERSION}-alpine AS prod-deps

WORKDIR /src
COPY package.json ./
COPY --from=deps /src/node_modules ./node_modules
RUN npm prune --omit=dev

FROM gcr.io/distroless/nodejs22-debian12:nonroot AS runtime

WORKDIR /app
COPY --from=builder --chown=65532:65532 /src/dist /app/dist
COPY --from=builder --chown=65532:65532 /src/migrations /app/migrations
COPY --from=builder --chown=65532:65532 /src/package.json /app/package.json
COPY --from=builder --chown=65532:65532 /src/README.md /app/README.md
COPY --from=builder --chown=65532:65532 /src/DESIGN.md /app/DESIGN.md
COPY --from=prod-deps --chown=65532:65532 /src/node_modules /app/node_modules

CMD ["-e", "import('/app/dist/index.js').then(() => console.log('customer-portal library image ready'))"]
