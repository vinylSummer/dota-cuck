GOBIN   := $(shell go env GOPATH)/bin
BUF     ?= $(GOBIN)/buf
UV      ?= uv
# Worker Python runs via uv (Python 3.10 + protobuf-3.20 line, fetched by uv).
UVRUN   := $(UV) run --project worker

SWAG    ?= $(GOBIN)/swag

NPM     ?= npm

.PHONY: proto proto-tools proto-lint proto-clean test test-go test-py test-fe docs

# Run all unit tests (control plane + worker + frontend).
test: test-go test-py test-fe

# DB-backed tests require PostgreSQL at POSTGRESQL_URL. with-test-db.sh spins up
# an ephemeral cluster for the run (or uses POSTGRESQL_URL if already set).
test-go:
	scripts/with-test-db.sh sh -c 'cd control-plane && go test ./...'

# uv fetches Python 3.10 and syncs worker deps on first run.
test-py:
	cd worker && $(UV) run pytest -q

# Frontend unit tests (Vitest + MSW). Installs deps on first run if absent.
test-fe:
	cd frontend && [ -d node_modules ] || $(NPM) install
	cd frontend && $(NPM) test

# Regenerate Go + Python stubs from proto/. Idempotent. grpcio-tools 1.48.x
# emits protobuf-3 gencode (no --pyi_out plugin in that line).
proto:
	$(BUF) generate proto
	@mkdir -p worker/gen
	$(UVRUN) python -m grpc_tools.protoc \
		-Iproto \
		--python_out=worker/gen \
		--grpc_python_out=worker/gen \
		proto/spectator/v1/worker.proto
	@# Make the Python generated tree an importable package.
	@find worker/gen -type d -exec touch {}/__init__.py \;
	@echo "proto: generated control-plane/gen and worker/gen"

# One-time bootstrap of the Go codegen toolchain. The worker's Python toolchain
# (grpcio-tools, Python 3.10) is provisioned by uv via `uv sync` / `uv run`.
proto-tools:
	go install github.com/bufbuild/buf/cmd/buf@v1.50.0
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.1
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
	go install github.com/swaggo/swag/cmd/swag@v1.16.4
	$(UV) sync --project worker

# Regenerate the OpenAPI spec (control-plane/docs/) from swaggo annotations.
# Run after changing handler annotations or the DTOs in internal/api. The
# generated docs/ package is committed so the binary builds without swag.
docs:
	cd control-plane && $(SWAG) init -g cmd/server/main.go -d ./ -o ./docs --parseInternal

proto-lint:
	$(BUF) lint proto

proto-clean:
	rm -rf control-plane/gen worker/gen
