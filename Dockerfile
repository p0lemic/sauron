# ── Build stage ───────────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS builder

WORKDIR /src

# Download dependencies first (better layer caching).
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# modernc.org/sqlite is pure Go — no CGO needed.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/profiler  ./cmd/profiler && \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/dashboard ./cmd/dashboard

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /out/profiler  ./profiler
COPY --from=builder /out/dashboard ./dashboard

# Data directory for SQLite (mount a volume here in dev).
RUN mkdir /data

EXPOSE 8080 9090

# Default: run the proxy. Override with:
#   command: ["./dashboard"]
CMD ["./profiler"]
