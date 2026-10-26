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

	srv, err := server.New(config, scheme.Handle, logger)
	if err != nil {
		return err
	}

	return srv.Serve(ctx)
}
