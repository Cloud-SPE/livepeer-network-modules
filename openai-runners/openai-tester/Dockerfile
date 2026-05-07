FROM node:22-alpine

WORKDIR /app
COPY package.json ./
RUN npm install --omit=dev

COPY test-*.mjs ./

ENV OPENAI_BASE_URL=http://localhost:8090/v1
ENV OPENAI_API_KEY=local-dev-no-auth

CMD ["node", "--test", "test-chat-completion.mjs"]
