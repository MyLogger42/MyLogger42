.PHONY: all build web run test docker-build clean tidy

# Default target builds frontend then backend so the embedded SPA is
# always fresh in the resulting binary.
all: build

# Build the React + Vite SPA into web/dist. The Go embed directive
# packages whatever is in that directory at compile time.
web:
	cd web && npm install && npm run build

# Build the Go binary. We invoke `web` first so the embed never picks
# up a stale dist tree. Override BIN to change the output path.
BIN ?= bin/mylogger42
build: web
	mkdir -p $(dir $(BIN))
	go build -o $(BIN) ./cmd/server

# Run the server locally. Reads .env if present (loaded by your shell —
# we don't pull in a dotenv loader in the Go binary).
run:
	go run ./cmd/server

# Run all Go tests. Frontend has no automated tests in v1.
test:
	go test ./...

# Tidy module deps and verify nothing's drifted.
tidy:
	go mod tidy

# Build the production Docker image. The image contains the Go binary
# and embedded SPA — no node/go runtime needed at deploy time.
IMAGE ?= mylogger42:latest
docker-build:
	docker build -t $(IMAGE) .

# Clean build artifacts. Does NOT remove ./data — that holds the SQLite
# db between runs. We restore web/dist/.gitkeep afterwards so the
# //go:embed directive at /embed.go can still resolve a non-empty dir
# even if you build the Go binary without rebuilding the SPA.
clean:
	rm -rf bin web/node_modules
	rm -rf web/dist
	mkdir -p web/dist
	touch web/dist/.gitkeep
