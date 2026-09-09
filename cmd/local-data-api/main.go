// Command local-data-api serves the AWS Aurora Serverless Data API against a
// local MySQL or PostgreSQL server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"

	"github.com/sonnh96/local-data-api/internal/config"
	"github.com/sonnh96/local-data-api/internal/resource"
	"github.com/sonnh96/local-data-api/internal/secrets"
	"github.com/sonnh96/local-data-api/internal/server"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	settings := config.Load()

	resources := resource.NewManager()
	defer resources.Close()

	secretManager := secrets.NewManager()
	settings.Apply(resources, secretManager)

	httpServer := &http.Server{
		Addr:              settings.Address,
		Handler:           server.New(resources, secretManager, logger).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("local-data-api listening",
			"address", settings.Address,
			"engine", settings.Engine,
			"host", settings.Host,
			"port", settings.Port,
			"resourceArn", settings.ResourceArn,
		)
		serverErrors <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	}
}
