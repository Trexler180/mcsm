.PHONY: all build clean test dev-api dev-agent dev-web

all: build

# ── Dev (run each in its own terminal) ──────────────────────────────
# Requires: Go toolchain, Node.js + pnpm, Java (for the MC servers the agent launches)
# No database install needed — API uses an embedded SQLite file (./mcsm.db).

dev-api:
	cd apps/api && \
	DATABASE_PATH="./mcsm.db" \
	JWT_SECRET="dev-secret" \
	go run ./cmd/server

dev-agent:
	cd apps/agent && \
	AGENT_TOKEN="dev-agent-token" \
	go run ./cmd/agent

dev-web:
	cd apps/web && pnpm dev

# ── Build ────────────────────────────────────────────────────────────
build: build-agent build-api build-web

build-agent:
	cd apps/agent && go build -o ../../bin/agent ./cmd/agent

build-api:
	cd apps/api && go build -o ../../bin/api ./cmd/server

build-web:
	cd apps/web && pnpm build

# ── Test ─────────────────────────────────────────────────────────────
test:
	cd apps/agent && go test ./...
	cd apps/api && go test ./...

# ── Clean ────────────────────────────────────────────────────────────
clean:
	rm -rf bin/ apps/web/dist/

clean-db:
	rm -f apps/api/mcsm.db apps/api/mcsm.db-shm apps/api/mcsm.db-wal
