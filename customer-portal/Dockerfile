FROM node:20-bookworm-slim

WORKDIR /app

COPY package.json tsconfig.json ./
RUN corepack enable && pnpm fetch || npm install --no-audit --no-fund

COPY src ./src
COPY test ./test
COPY migrations ./migrations

RUN npm install --no-audit --no-fund && npm run build

CMD ["node", "--test", "dist/test/**/*.test.js"]
