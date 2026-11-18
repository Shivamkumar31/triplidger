FROM golang:1.27-bookworm AS dev
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN go mod tidy
EXPOSE 8080
CMD ["go", "run", "."]

FROM dev AS build
RUN go test ./... && CGO_ENABLED=0 go build -trimpath -o /tripledger .
FROM debian:bookworm-slim AS production
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/* && useradd -u 10001 -m app
COPY --from=build /tripledger /usr/local/bin/tripledger
USER app
EXPOSE 8080
ENTRYPOINT ["tripledger"]
