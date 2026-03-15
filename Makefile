.PHONY: build run-proxy run-dashboard dev dev-proxy dev-dashboard test docker-build clean

# ── Defaults (override via env or CLI) ────────────────────────────────────────
UPSTREAM        ?= http://localhost:3000
PROXY_PORT      ?= 8080
DASHBOARD_PORT  ?= 9090
STORAGE_DSN     ?= profiler.db
IMAGE           ?= sauron
TAG             ?= latest

# ── Build ─────────────────────────────────────────────────────────────────────
build:
	@mkdir -p bin
	go build -o bin/profiler  ./cmd/profiler
	go build -o bin/dashboard ./cmd/dashboard

# ── Run (no hot reload) ───────────────────────────────────────────────────────
run-proxy: build
	./bin/profiler --upstream $(UPSTREAM) --port $(PROXY_PORT) --storage-dsn $(STORAGE_DSN)

run-dashboard: build
	./bin/dashboard --listen :$(DASHBOARD_PORT) --storage-dsn $(STORAGE_DSN)

# ── Dev (hot reload) ──────────────────────────────────────────────────────────
# Requires: brew install watchexec   (macOS)
#           cargo install watchexec-cli  (Linux)

dev: _check-watchexec
	@echo "→ Starting proxy (:$(PROXY_PORT)) and dashboard (:$(DASHBOARD_PORT)) with hot reload"
	@trap 'kill %1 %2 2>/dev/null; exit 0' INT TERM; \
	  $(MAKE) dev-proxy & \
	  $(MAKE) dev-dashboard & \
	  wait

dev-proxy: _check-watchexec
	watchexec -e go,yaml,toml \
	  --restart \
	  -- sh -c 'go build -o tmp/profiler ./cmd/profiler && \
	            ./tmp/profiler --upstream $(UPSTREAM) --port $(PROXY_PORT) --storage-dsn $(STORAGE_DSN)'

dev-dashboard: _check-watchexec
	watchexec -e go,html,css,js,yaml,toml \
	  --restart \
	  -- sh -c 'go build -o tmp/dashboard ./cmd/dashboard && \
	            ./tmp/dashboard --listen :$(DASHBOARD_PORT) --storage-dsn $(STORAGE_DSN)'

_check-watchexec:
	@command -v watchexec >/dev/null 2>&1 || { \
	  echo ""; \
	  echo "  'watchexec' not found. Install it with:"; \
	  echo "    macOS:  brew install watchexec"; \
	  echo "    Linux:  cargo install watchexec-cli"; \
	  echo ""; \
	  exit 1; \
	}
	@mkdir -p tmp

# ── Tests ─────────────────────────────────────────────────────────────────────
test:
	go test ./...

# ── Docker ────────────────────────────────────────────────────────────────────
docker-build:
	docker build -t $(IMAGE):$(TAG) .

docker-push:
	docker push $(IMAGE):$(TAG)

# ── Misc ──────────────────────────────────────────────────────────────────────
clean:
	rm -rf bin tmp
