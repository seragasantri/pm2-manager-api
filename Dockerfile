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

# Runtime: Alpine + Node/PM2 CLI so the API can talk to the host PM2 daemon
# via mounted PM2_HOME (rpc.sock/pub.sock). Docker still uses the Go SDK + socket.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates nodejs npm \
  && npm install -g pm2@latest \
  && pm2 -v

WORKDIR /app

COPY --from=builder /out/server  /app/server
COPY --from=builder /out/migrate /app/migrate
COPY --from=builder /out/seed    /app/seed
COPY .env.example /app/.env.example

EXPOSE 3003

CMD ["/app/server"]
