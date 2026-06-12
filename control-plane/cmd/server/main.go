// Command server is the control-plane entry point. It runs three listeners
// concurrently: a gRPC server for workers, an HTTP server for the REST API,
// and the same HTTP mux serves the WebSocket hub at /ws.
//
// V1: auth (register/login) is implemented and backed by Postgres; the
// remaining HTTP handlers return 501 until their feature steps land. The
// wiring, graceful shutdown, and worker stream handling are real.
package main

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/vinylSummer/dota-cuck/internal/auth"
	"github.com/vinylSummer/dota-cuck/internal/store"
	"github.com/vinylSummer/dota-cuck/internal/workers"
	"google.golang.org/grpc"
)

// @title        Dota Spectator Control Plane API
// @version      1.0
// @description  Self-hosted service to spectate live Dota 2 matches of Steam friends.
// @description  Auth (register/login) is live; other handlers return 501 until their feature steps land.
// @BasePath     /api
func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// mustEnv returns the value of key or logs a fatal error if it is unset. Used
// for secrets that have no safe default.
func mustEnv(log *slog.Logger, key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Error("required environment variable not set", "key", key)
		os.Exit(1)
	}
	return v
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	grpcAddr := env("GRPC_LISTEN_ADDR", ":42010")
	httpAddr := env("HTTP_LISTEN_ADDR", ":42000")
	databaseURL := mustEnv(log, "DATABASE_URL")
	jwtSecret := mustEnv(log, "JWT_SECRET")
	credentialPepper := mustEnv(log, "CREDENTIAL_PEPPER")

	const sessionTTL = 24 * time.Hour

	hasher, err := auth.NewHasher([]byte(credentialPepper))
	if err != nil {
		log.Error("init password hasher", "err", err)
		os.Exit(1)
	}
	tokens, err := auth.NewTokenManager([]byte(jwtSecret), sessionTTL)
	if err != nil {
		log.Error("init token manager", "err", err)
		os.Exit(1)
	}
	// Credential keys live as long as the token they were minted for.
	keys := auth.NewKeyCache(sessionTTL)

	dbCtx, dbCancel := context.WithTimeout(context.Background(), 10*time.Second)
	db, err := store.New(dbCtx, databaseURL)
	dbCancel()
	if err != nil {
		log.Error("connect to database", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	reg := workers.NewRegistry()
	workerSrv := workers.NewServer(reg, log)
	grpcServer := grpc.NewServer()
	pb.RegisterControlPlaneServiceServer(grpcServer, workerSrv)

	hub := api.NewHub(log)
	httpServer := &http.Server{
		Addr: httpAddr,
		Handler: api.NewServer(api.Deps{
			Hub:           hub,
			Users:         db.Users,
			SteamAccounts: db.SteamAccounts,
			Friends:       friendsProvider{worker: workerSrv},
			Hasher:        hasher,
			Tokens:        tokens,
			Keys:          keys,
		}).Router(),
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

// friendsProvider adapts the worker gRPC server to api.FriendsProvider: it sends
// a ListFriends command over the worker stream and maps the FriendsResult.
type friendsProvider struct {
	worker *workers.Server
}

func (f friendsProvider) ListFriends(ctx context.Context, username, password string, sentry []byte) (*api.FriendList, error) {
	res, err := f.worker.ListFriends(ctx, username, password, sentry)
	if err != nil {
		return nil, err
	}
	if e := res.GetError(); e != nil {
		return nil, fmt.Errorf("worker: %s: %s", e.GetCode(), e.GetMessage())
	}
	list := &api.FriendList{OwnerSteamID: res.GetOwnerSteamId()}
	for _, fr := range res.GetFriends() {
		list.Friends = append(list.Friends, api.FriendStatus{
			SteamID:     fr.GetSteamId(),
			PersonaName: fr.GetPersonaName(),
			Online:      fr.GetOnline(),
			InMatch:     fr.GetInMatch(),
		})
	}
	return list, nil
}
