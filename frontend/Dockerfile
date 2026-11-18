# Local development stage
FROM node:24-bookworm-slim AS dev

WORKDIR /app

ENV NEXT_TELEMETRY_DISABLED=1

COPY package*.json ./
RUN npm install

COPY . .

EXPOSE 3000

CMD ["npm", "run", "dev"]


# Production stage for Render
FROM dev AS production

# Available during next build for the API rewrite.
ARG API_INTERNAL_URL
ENV API_INTERNAL_URL=${API_INTERNAL_URL}

ENV NODE_ENV=production

RUN npm run build

CMD ["npm", "start"]