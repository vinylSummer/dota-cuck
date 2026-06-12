GOBIN   := $(shell go env GOPATH)/bin
BUF     ?= $(GOBIN)/buf
VENV    ?= .venv
PY      := $(VENV)/bin/python

.PHONY: proto proto-tools proto-lint proto-clean test test-go test-py

# Run all unit tests (control plane + worker).
test: test-go test-py

test-go:
	cd control-plane && go test ./...

# Uses the worker venv; create it with: python3 -m venv worker/.venv && \
#   worker/.venv/bin/pip install -r worker/requirements-dev.txt
test-py:
	cd worker && .venv/bin/python -m pytest -q

# Regenerate Go + Python stubs from proto/. Idempotent.
proto:
	$(BUF) generate proto
	@mkdir -p worker/gen
	$(PY) -m grpc_tools.protoc \
		-Iproto \
		--python_out=worker/gen \
		--pyi_out=worker/gen \
		--grpc_python_out=worker/gen \
		proto/spectator/v1/worker.proto
	@# Make the Python generated tree an importable package.
	@find worker/gen -type d -exec touch {}/__init__.py \;
	@echo "proto: generated control-plane/gen and worker/gen"

# One-time bootstrap of the codegen toolchain (buf, Go plugins, Python venv).
proto-tools:
	go install github.com/bufbuild/buf/cmd/buf@v1.50.0
	go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.1
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
	python3 -m venv $(VENV)
	$(VENV)/bin/pip install --upgrade pip
	$(VENV)/bin/pip install grpcio-tools==1.81.0

proto-lint:
	$(BUF) lint proto

proto-clean:
	rm -rf control-plane/gen worker/gen
