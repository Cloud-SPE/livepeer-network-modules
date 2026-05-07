FROM node:22-alpine

WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm install --omit=dev

COPY *.mjs *.json ./

CMD ["node", "test-transcode.mjs", "quick"]
