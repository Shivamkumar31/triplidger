FROM node:24-bookworm-slim AS dev
WORKDIR /app
ENV NEXT_TELEMETRY_DISABLED=1
COPY package*.json ./
RUN npm install
COPY . .
EXPOSE 3000
CMD ["npm", "run", "dev"]
FROM dev AS production
RUN npm run build
CMD ["npm", "start"]
