# Control plane: build the Go binary, then ship it on a slim runtime with the
# migration files it applies on boot. CGO is off so the binary runs on distroless.
FROM golang:1.26-bookworm AS build
WORKDIR /src

# Cache deps first.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/control-plane ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
# Migrations are read at boot from MIGRATIONS_DIR (default ./db/migrations).
COPY --from=build /src/db/migrations /app/db/migrations
COPY --from=build /out/control-plane /app/control-plane

# HTTP API + gRPC (workers). nginx terminates TLS on the host and proxies here.
EXPOSE 42000 42010
USER nonroot:nonroot
ENTRYPOINT ["/app/control-plane"]
