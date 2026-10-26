package main

import (
	"context"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun(t *testing.T) {
	t.Run("does not report a shutdown signal when startup fails", func(t *testing.T) {
		taken, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer taken.Close()
		original := config
		t.Cleanup(func() { config = original })
		config.Addr = taken.Addr().String()

		messages := make(chan string, 10)
		err = run(slog.New(messageHandler(messages)))

		require.Error(t, err)
		// The signal callback would run asynchronously, so give it time to.
		time.Sleep(100 * time.Millisecond)
		for range len(messages) {
			assert.NotContains(t, <-messages, "shutdown signal received")
		}
	})
}

// messageHandler is a slog.Handler that sends each log message to a channel.
type messageHandler chan<- string

func (h messageHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h messageHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h messageHandler) WithGroup(string) slog.Handler            { return h }

func (h messageHandler) Handle(_ context.Context, r slog.Record) error {
	h <- r.Message
	return nil
}
