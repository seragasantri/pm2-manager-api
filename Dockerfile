# Build stage
FROM golang:1.22-alpine AS builder

# CGO is required by the Docker SDK (sqlite dep), but we don't actually use sqlite.
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

# Runtime stage
FROM alpine:3.20
RUN apk add --no-cache ca-certificates bash docker-cli
WORKDIR /app

COPY --from=builder /out/server  /app/server
COPY --from=builder /out/migrate /app/migrate
COPY --from=builder /out/seed    /app/seed
COPY .env.example /app/.env.example

EXPOSE 3003

# Run the API server. The migrate/seed binaries are present on the image
# and can be run manually before starting the server.
CMD ["/app/server"]