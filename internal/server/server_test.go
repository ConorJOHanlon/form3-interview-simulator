package server_test

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/form3tech-oss/interview-simulator/internal/server"
	"github.com/form3tech-oss/interview-simulator/internal/server/servertest"
)

// echo responds to every request with the request itself.
func echo(_ context.Context, request string) string {
	return "echo:" + request
}

func TestNew(t *testing.T) {
	t.Run("fails when the address is already in use", func(t *testing.T) {
		taken, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer taken.Close()

		_, err = server.New(server.Config{Addr: taken.Addr().String()}, echo, slog.New(slog.DiscardHandler))

		assert.Error(t, err)
	})
}

func TestServer(t *testing.T) {
	t.Run("responds to each request on a connection in order", func(t *testing.T) {
		srv := servertest.Start(t, server.Config{}, echo)
		client := servertest.Dial(t, srv.Addr)

		for _, request := range []string{"first", "second", "third"} {
			client.Send(request)
			assert.Equal(t, "echo:"+request, client.Receive())
		}
	})

	t.Run("serves connections concurrently", func(t *testing.T) {
		const connections = 5

		// Each request waits until every connection has a request in flight,
		// which only happens if connections are served concurrently.
		var inFlight sync.WaitGroup
		inFlight.Add(connections)
		allInFlight := make(chan struct{})
		go func() {
			inFlight.Wait()
			close(allInFlight)
		}()
		handler := func(_ context.Context, _ string) string {
			inFlight.Done()
			select {
			case <-allInFlight:
				return "concurrent"
			case <-time.After(time.Second):
				return "serialised"
			}
		}

		srv := servertest.Start(t, server.Config{}, handler)
		clients := make([]*servertest.Client, connections)
		for i := range clients {
			clients[i] = servertest.Dial(t, srv.Addr)
			clients[i].Send("request")
		}

		for _, client := range clients {
			assert.Equal(t, "concurrent", client.Receive())
		}
	})

	t.Run("stops accepting connections once shut down", func(t *testing.T) {
		srv := servertest.Start(t, server.Config{}, echo)

		srv.Shutdown()
		<-srv.Stopped()

		_, err := net.Dial("tcp", srv.Addr)
		assert.Error(t, err)
	})
}

func TestRequestFraming(t *testing.T) {
	t.Run("strips a trailing carriage return", func(t *testing.T) {
		client := servertest.Dial(t, servertest.Start(t, server.Config{}, echo).Addr)

		client.Send("request\r")

		assert.Equal(t, "echo:request", client.Receive())
	})

	t.Run("passes an empty line to the handler", func(t *testing.T) {
		client := servertest.Dial(t, servertest.Start(t, server.Config{}, echo).Addr)

		client.Send("")

		assert.Equal(t, "echo:", client.Receive())
	})

	t.Run("reassembles a request split across writes", func(t *testing.T) {
		client := servertest.Dial(t, servertest.Start(t, server.Config{}, echo).Addr)

		client.SendRaw("req")
		client.SendRaw("uest\n")

		assert.Equal(t, "echo:request", client.Receive())
	})

	t.Run("splits several requests sent in one write", func(t *testing.T) {
		client := servertest.Dial(t, servertest.Start(t, server.Config{}, echo).Addr)

		client.SendRaw("first\nsecond\n")

		assert.Equal(t, "echo:first", client.Receive())
		assert.Equal(t, "echo:second", client.Receive())
	})

	t.Run("discards a request without a terminator when the client closes", func(t *testing.T) {
		client := servertest.Dial(t, servertest.Start(t, server.Config{}, echo).Addr)

		client.SendRaw("request")
		client.CloseWrite()

		client.RequireClosed()
	})

	t.Run("accepts a request of the maximum length", func(t *testing.T) {
		client := servertest.Dial(t, servertest.Start(t, server.Config{}, echo).Addr)
		request := strings.Repeat("a", server.MaxRequestLength-1)

		client.Send(request)

		assert.Equal(t, "echo:"+request, client.Receive())
	})

	t.Run("closes the connection on a request over the maximum length", func(t *testing.T) {
		client := servertest.Dial(t, servertest.Start(t, server.Config{}, echo).Addr)

		client.Send(strings.Repeat("a", server.MaxRequestLength))

		client.RequireClosed()
	})
}
