# Build stage
FROM golang:1.25-alpine AS builder

# Disable CGO for a smaller, statically-linked binary.
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64

WORKDIR /src

# Cache deps
COPY go.mod go.sum* ./
RUN go mod download

# Build all three binaries
COPY . .
RUN go build -ldflags="-s -w" -o /out/server  ./cmd/server
RUN go build -ldflags="-s -w" -o /out/migrate ./cmd/migrate
RUN go build -ldflags="-s -w" -o /out/seed    ./cmd/seed

# Runtime stage — minimal: only the Go binaries + ca-certificates.
# We use scratch-like alpine:no need for bash, docker-cli, or shell tools
# because:
#   - The Docker SDK (Go) talks to /var/run/docker.sock directly (no CLI).
#   - PM2 CLI is invoked on the HOST (not inside this container) via exec.
#   - Terminal exec for users runs on the HOST (chrooted to their app cwd).
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
WORKDIR /app

COPY --from=builder /out/server  /app/server
COPY --from=builder /out/migrate /app/migrate
COPY --from=builder /out/seed    /app/seed
COPY .env.example /app/.env.example

EXPOSE 3003

CMD ["/app/server"]