// Command simulator runs the payment scheme simulator.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
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
	// The simulator takes no flags, but parsing gives -h a usage message and
	// rejects anything else rather than silently starting a server.
	flag.Usage = usage
	flag.Parse()
	if flag.NArg() > 0 {
		fmt.Fprintf(flag.CommandLine.Output(), "unexpected argument: %s\n", flag.Arg(0))
		flag.Usage()
		os.Exit(2)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if err := run(logger); err != nil {
		logger.Error("simulator failed", "error", err)
		os.Exit(1)
	}
}

func usage() {
	_, port, _ := net.SplitHostPort(config.Addr)
	fmt.Fprintf(flag.CommandLine.Output(), `💸 Payment Scheme Simulator

Simulates the payment scheme over TCP, for testing payment clients.

USAGE
  simulator [-h]

SETTINGS (set in cmd/simulator/main.go)
  Address        %[1]s
  Grace period   %[2]v

PROTOCOL
  Request        PAYMENT|<amount>, terminated by a newline
  Response       RESPONSE|ACCEPTED|<reason> or RESPONSE|REJECTED|<reason>
  Delay          amounts over 100 take <amount> ms, up to 10s

TRY IT
  $ nc localhost %[3]s
  PAYMENT|500
  RESPONSE|ACCEPTED|Transaction processed

SHUTDOWN
  Ctrl+C         stop accepting connections; cancel unfinished payments after %[2]v
  Ctrl+C again   exit immediately

Every request is logged to stderr with its response and duration.
`, config.Addr, config.GracePeriod, port)
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
