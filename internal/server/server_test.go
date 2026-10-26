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

// These tests use stub handlers instead of the payment scheme, so connection
// handling and shutdown are tested in isolation, with each test controlling
// exactly when its requests complete. End-to-end tests with the real scheme
// live in cmd/simulator.

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

	t.Run("answers a complete request after the client stops sending", func(t *testing.T) {
		client := servertest.Dial(t, servertest.Start(t, server.Config{}, echo).Addr)

		// Like `echo request | nc -N`: send, then close the sending side.
		client.Send("request")
		client.CloseWrite()

		assert.Equal(t, "echo:request", client.Receive())
		client.RequireClosed()
	})

	t.Run("discards a request without a terminator when the client closes", func(t *testing.T) {
		client := servertest.Dial(t, servertest.Start(t, server.Config{}, echo).Addr)

		client.SendRaw("complete\npartial")
		client.CloseWrite()

		assert.Equal(t, "echo:complete", client.Receive())
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

func TestGracefulShutdown(t *testing.T) {
	const (
		// gracePeriod is used by tests that wait for it to expire.
		gracePeriod = 200 * time.Millisecond
		// longGracePeriod is used by tests that must finish well before it
		// expires.
		longGracePeriod = 10 * time.Second
		tolerance       = 50 * time.Millisecond
	)

	t.Run("refuses new connections but keeps serving existing ones", func(t *testing.T) {
		srv := servertest.Start(t, server.Config{GracePeriod: longGracePeriod}, echo)
		client := connect(t, srv)

		srv.Shutdown()

		assert.Eventually(t, func() bool {
			conn, err := net.Dial("tcp", srv.Addr)
			if err != nil {
				return true
			}
			conn.Close()
			return false
		}, time.Second, 10*time.Millisecond, "new connections are still accepted")
		client.Send("during shutdown")
		assert.Equal(t, "echo:during shutdown", client.Receive())
		requireRunning(t, srv)
	})

	t.Run("lets an in-flight request complete within the grace period", func(t *testing.T) {
		handler := newBlockingHandler()
		srv := servertest.Start(t, server.Config{GracePeriod: longGracePeriod}, handler.handle)
		client := servertest.Dial(t, srv.Addr)
		client.Send("request")
		handler.waitForRequest(t)

		srv.Shutdown()
		handler.releaseAll()

		assert.Equal(t, "done:request", client.Receive())
	})

	t.Run("cancels an in-flight request when the grace period expires", func(t *testing.T) {
		handler := newBlockingHandler()
		srv := servertest.Start(t, server.Config{GracePeriod: gracePeriod}, handler.handle)
		client := servertest.Dial(t, srv.Addr)
		client.Send("request")
		handler.waitForRequest(t)

		start := time.Now()
		srv.Shutdown()
		response := client.Receive()
		elapsed := time.Since(start)

		assert.Equal(t, "cancelled:request", response)
		assert.GreaterOrEqual(t, elapsed, gracePeriod, "cancelled before the grace period expired")
		assert.Less(t, elapsed, gracePeriod+tolerance, "cancelled too long after the grace period expired")
		client.RequireClosed()
		requireStoppedWithin(t, srv, tolerance)
	})

	t.Run("cancels in-flight requests on every connection", func(t *testing.T) {
		const connections = 3
		handler := newBlockingHandler()
		srv := servertest.Start(t, server.Config{GracePeriod: gracePeriod}, handler.handle)
		clients := make([]*servertest.Client, connections)
		for i := range clients {
			clients[i] = servertest.Dial(t, srv.Addr)
			clients[i].Send("request")
			handler.waitForRequest(t)
		}

		srv.Shutdown()

		for _, client := range clients {
			assert.Equal(t, "cancelled:request", client.Receive())
		}
		requireStoppedWithin(t, srv, gracePeriod+tolerance)
	})

	t.Run("closes idle connections without a response when the grace period expires", func(t *testing.T) {
		srv := servertest.Start(t, server.Config{GracePeriod: gracePeriod}, echo)
		client := connect(t, srv)

		start := time.Now()
		srv.Shutdown()
		client.RequireClosed()
		elapsed := time.Since(start)

		assert.GreaterOrEqual(t, elapsed, gracePeriod, "closed before the grace period expired")
		assert.Less(t, elapsed, gracePeriod+tolerance, "closed too long after the grace period expired")
		requireStoppedWithin(t, srv, tolerance)
	})

	t.Run("cancels in-flight requests as soon as shutdown starts without a grace period", func(t *testing.T) {
		for _, gracePeriod := range []time.Duration{0, -time.Second} {
			t.Run(gracePeriod.String(), func(t *testing.T) {
				handler := newBlockingHandler()
				srv := servertest.Start(t, server.Config{GracePeriod: gracePeriod}, handler.handle)
				client := servertest.Dial(t, srv.Addr)
				client.Send("request")
				handler.waitForRequest(t)

				srv.Shutdown()

				assert.Equal(t, "cancelled:request", client.Receive())
				requireStoppedWithin(t, srv, tolerance)
			})
		}
	})

	t.Run("serves a request completed during the grace period", func(t *testing.T) {
		srv := servertest.Start(t, server.Config{GracePeriod: longGracePeriod}, echo)
		client := connect(t, srv)
		client.SendRaw("req")

		srv.Shutdown()
		client.SendRaw("uest\n")

		assert.Equal(t, "echo:request", client.Receive())
	})

	t.Run("discards a partially received request when the grace period expires", func(t *testing.T) {
		srv := servertest.Start(t, server.Config{GracePeriod: gracePeriod}, echo)
		client := connect(t, srv)
		client.SendRaw("partial request")

		srv.Shutdown()

		client.RequireClosed()
		requireStoppedWithin(t, srv, gracePeriod+tolerance)
	})

	t.Run("stops as soon as every connection has closed", func(t *testing.T) {
		srv := servertest.Start(t, server.Config{GracePeriod: longGracePeriod}, echo)
		client := connect(t, srv)

		srv.Shutdown()
		requireRunning(t, srv)
		client.Close()

		requireStoppedWithin(t, srv, tolerance)
	})

	t.Run("stops immediately when there are no connections", func(t *testing.T) {
		srv := servertest.Start(t, server.Config{GracePeriod: longGracePeriod}, echo)

		srv.Shutdown()

		requireStoppedWithin(t, srv, tolerance)
		_, err := net.Dial("tcp", srv.Addr)
		assert.Error(t, err, "server still accepts connections after stopping")
	})
}

// blockingHandler holds each request until it is released or its context is
// cancelled, so tests control exactly when requests complete.
type blockingHandler struct {
	// received gets each request as soon as the handler starts on it.
	received chan string
	release  chan struct{}
}

func newBlockingHandler() *blockingHandler {
	return &blockingHandler{received: make(chan string, 10), release: make(chan struct{})}
}

func (h *blockingHandler) handle(ctx context.Context, request string) string {
	h.received <- request
	select {
	case <-h.release:
		return "done:" + request
	case <-ctx.Done():
		return "cancelled:" + request
	}
}

func (h *blockingHandler) releaseAll() {
	close(h.release)
}

// connect returns a client the server has already accepted, proven by a
// completed request, so a shutdown that follows can't race the accept.
func connect(t *testing.T, srv *servertest.Server) *servertest.Client {
	t.Helper()
	client := servertest.Dial(t, srv.Addr)
	client.Send("ping")
	require.Equal(t, "echo:ping", client.Receive())
	return client
}

func requireRunning(t *testing.T, srv *servertest.Server) {
	t.Helper()
	select {
	case <-srv.Stopped():
		t.Fatal("server stopped before its grace period expired")
	default:
	}
}

func requireStoppedWithin(t *testing.T, srv *servertest.Server, timeout time.Duration) {
	t.Helper()
	select {
	case <-srv.Stopped():
		require.NoError(t, srv.Err())
	case <-time.After(timeout):
		t.Fatalf("server did not stop within %v", timeout)
	}
}

// waitForRequest bounds synchronization too: a server regression should fail
// the test instead of hanging before the client's read timeout can help.
func (h *blockingHandler) waitForRequest(t *testing.T) string {
	t.Helper()
	select {
	case request := <-h.received:
		return request
	case <-time.After(time.Second):
		t.Fatal("handler did not receive the request within one second")
		return ""
	}
}
