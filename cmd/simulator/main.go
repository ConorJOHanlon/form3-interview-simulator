// Command simulator runs the payment scheme simulator.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/form3tech-oss/interview-simulator/internal/scheme"
	"github.com/form3tech-oss/interview-simulator/internal/server"
)

var config = server.Config{
	Addr: ":8080",
	// Must stay below the platform's termination timeout (30s by default on
	// Kubernetes), or in-flight requests are killed without a response.
	GracePeriod: 3 * time.Second,
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if err := run(logger); err != nil {
		logger.Error("simulator failed", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Restore default signal handling once shutdown starts, so a second
	// signal exits immediately instead of waiting out the grace period.
	stopSignalCallback := context.AfterFunc(ctx, func() {
		stop()
		logger.Info("shutdown signal received, send it again to exit immediately")
	})

	// Disable the callback before deferred stop cancels ctx on an error.
	defer stopSignalCallback()

	srv, err := server.New(config, scheme.Handle, logger)
	if err != nil {
		return err
	}

	return srv.Serve(ctx)
}
