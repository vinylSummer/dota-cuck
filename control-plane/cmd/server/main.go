// Command server is the control-plane entry point. It runs three listeners
// concurrently: a gRPC server for workers, an HTTP server for the REST API,
// and the same HTTP mux serves the WebSocket hub at /ws.
//
// V1 skeleton: HTTP handlers return 501 and no DB queries run yet. The wiring,
// graceful shutdown, and worker stream handling are real.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/vinylSummer/dota-cuck/docs" // generated OpenAPI spec (swag init)
	pb "github.com/vinylSummer/dota-cuck/gen/spectator/v1"
	"github.com/vinylSummer/dota-cuck/internal/api"
	"github.com/vinylSummer/dota-cuck/internal/workers"
	"google.golang.org/grpc"
)

// @title        Dota Spectator Control Plane API
// @version      1.0
// @description  Self-hosted service to spectate live Dota 2 matches of Steam friends.
// @description  All handlers are skeleton stubs (501) until their feature steps land.
// @BasePath     /api
func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	grpcAddr := env("GRPC_LISTEN_ADDR", ":42010")
	httpAddr := env("HTTP_LISTEN_ADDR", ":42000")

	reg := workers.NewRegistry()
	grpcServer := grpc.NewServer()
	pb.RegisterControlPlaneServiceServer(grpcServer, workers.NewServer(reg, log))

	hub := api.NewHub(log)
	httpServer := &http.Server{
		Addr:    httpAddr,
		Handler: api.NewServer(hub).Router(),
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 2)

	go func() {
		lis, err := net.Listen("tcp", grpcAddr)
		if err != nil {
			errCh <- err
			return
		}
		log.Info("gRPC listening", "addr", grpcAddr)
		errCh <- grpcServer.Serve(lis)
	}()

	go func() {
		log.Info("HTTP listening", "addr", httpAddr)
		if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-errCh:
		log.Error("server error", "err", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Warn("http shutdown", "err", err)
	}
	grpcServer.GracefulStop()
	log.Info("stopped")
}
