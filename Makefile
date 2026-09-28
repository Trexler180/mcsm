.PHONY: all build clean test dev-api dev-agent dev-web dev-mcp

all: build

# ── Dev (run each in its own terminal) ──────────────────────────────
# Requires: Go toolchain, Node.js + pnpm, Java (for the MC servers the agent launches)
# No database install needed — API uses an embedded SQLite file (./mcsm.db).

dev-api:
	cd apps/api && \
	MCSM_DEV_MODE="1" \
	DATABASE_PATH="./mcsm.db" \
	JWT_SECRET="dev-secret" \
	go run ./cmd/server

# The API with remote-agent (MCP over OAuth) access enabled. Needs dev-web
# running too: the consent screen is a dashboard route, and Vite is the single
# origin the browser, the SPA and the MCP endpoint share locally. Prints no
# credential and writes to no agent config — connecting is a browser click.
# Windows equivalent: scripts/Start-McpDemo.ps1
#
# APP_BASE_PATH is deliberately left unset rather than set to "/". MSYS2 shells
# (Git Bash) rewrite a lone "/" in an environment variable into the MSYS install
# root before a native binary sees it, which turned the consent link into
# http://localhost:3000/C:/Program%20Files/Git/mcp-consent. Unset already means
# "/", so the explicit value only ever added a way to get it wrong.
dev-mcp:
	@echo "Connect a client with:"
	@echo "    claude mcp add --transport http servermanager http://localhost:3000/api/v1/mcp"
	@echo "Then approve in the browser. Run 'make dev-web' in another terminal."
	cd apps/api && \
	MCSM_DEV_MODE="1" \
	DATABASE_PATH="./mcsm.db" \
	JWT_SECRET="dev-secret" \
	MCP_PUBLIC_ORIGIN="http://localhost:3000" \
	go run ./cmd/server

dev-agent:
	cd apps/agent && \
	MCSM_DEV_MODE="1" \
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
