# syntax=docker/dockerfile:1.7
#
# vtuber-runner image — Python service + avatar-renderer dist + Chromium.
#
# Three stages (per plan 0013-vtuber §4.3 + OQ1 rebuild-from-source lock):
#   1. renderer-builder  — node:20 + pnpm; `vite build` emits
#                          avatar-renderer/dist/.
#   2. python-deps       — python:3.12 + uv; `uv sync` resolves the
#                          `session-runner` package deps into a venv.
#   3. final             — python:3.12-slim + venv from (2) + dist from
#                          (1) + Playwright chromium-headless-shell.
#
# Build context expected at the monorepo root:
#   docker build -t vtuber-runner:dev -f vtuber-runner/Dockerfile .
#
# Image shape mirrors livepeer-vtuber-project/session-runner/Dockerfile.

# ─── Stage 1: avatar-renderer dist build ──────────────────────────────────
FROM node:20-slim AS renderer-builder

WORKDIR /workspace

RUN corepack enable

COPY package.json pnpm-workspace.yaml pnpm-lock.yaml* ./
COPY vtuber-runner/avatar-renderer/package.json vtuber-runner/avatar-renderer/

RUN pnpm install --frozen-lockfile --filter @livepeer-rewrite/avatar-renderer || \
    pnpm install --filter @livepeer-rewrite/avatar-renderer

COPY vtuber-runner/avatar-renderer/ vtuber-runner/avatar-renderer/
RUN pnpm --filter @livepeer-rewrite/avatar-renderer build

RUN test -f vtuber-runner/avatar-renderer/dist/index.html


# ─── Stage 2: Python venv with session-runner deps ────────────────────────
FROM python:3.12-slim AS python-deps

COPY --from=ghcr.io/astral-sh/uv:0.5.4 /uv /usr/local/bin/uv

WORKDIR /build

COPY vtuber-runner/pyproject.toml vtuber-runner/uv.lock* ./

RUN mkdir -p src/session_runner && \
    touch src/session_runner/__init__.py

ENV UV_PROJECT_ENVIRONMENT=/build/.venv \
    UV_LINK_MODE=copy
RUN uv sync --no-dev --frozen 2>/dev/null || uv sync --no-dev


# ─── Stage 3: runtime ─────────────────────────────────────────────────────
FROM python:3.12-slim AS runtime

# Playwright system libs — same set as
# `livepeer-vtuber-project/session-runner/Dockerfile`.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        fonts-liberation \
        fonts-noto-color-emoji \
        ffmpeg \
        libasound2 \
        libatk-bridge2.0-0 \
        libatk1.0-0 \
        libatspi2.0-0 \
        libcairo2 \
        libcups2 \
        libdbus-1-3 \
        libdrm2 \
        libgbm1 \
        libglib2.0-0 \
        libnspr4 \
        libnss3 \
        libpango-1.0-0 \
        libwayland-client0 \
        libx11-6 \
        libxcb1 \
        libxcomposite1 \
        libxdamage1 \
        libxext6 \
        libxfixes3 \
        libxkbcommon0 \
        libxrandr2 \
        xdg-utils \
    && rm -rf /var/lib/apt/lists/*

RUN groupadd --system --gid 65532 nonroot && \
    useradd --system --uid 65532 --gid nonroot --create-home --shell /usr/sbin/nologin nonroot

WORKDIR /app

COPY --from=python-deps /build/.venv /build/.venv
COPY --from=renderer-builder /workspace/vtuber-runner/avatar-renderer/dist /app/avatar-renderer/dist

COPY vtuber-runner/ /build/vtuber-runner/

ENV PLAYWRIGHT_BROWSERS_PATH=/app/.playwright
RUN /build/.venv/bin/playwright install chromium-headless-shell && \
    chown -R nonroot:nonroot /app /build

ENV SESSION_RUNNER_HTTP_HOST=0.0.0.0 \
    SESSION_RUNNER_HTTP_PORT=8080 \
    SESSION_RUNNER_RENDERER=chromium \
    SESSION_RUNNER_RENDERER_DIST=/app/avatar-renderer/dist \
    PATH=/build/.venv/bin:$PATH

USER nonroot:nonroot

EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=5s --retries=5 --start-period=15s \
    CMD ["python", "-c", "import urllib.request,sys; sys.exit(0 if urllib.request.urlopen('http://127.0.0.1:8080/api/health').status == 200 else 1)"]

ENTRYPOINT ["/build/.venv/bin/session-runner"]
