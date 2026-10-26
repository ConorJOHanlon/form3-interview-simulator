package server

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests need the server's internals, to make accepting connections or
// writing responses fail, or to control how requests arrive across reads.
// Everything else is tested through the public API in server_test.go.

func TestServeInternals(t *testing.T) {
	t.Run("cancels in-flight requests immediately when accepting connections fails", func(t *testing.T) {
		handler, received := newTestHandler()
		srv := newTestServer(t, time.Minute, handler)
		_, wait := serve(t, srv)
		conn, reader := dial(t, srv)
		send(t, conn, "slow\n")
		<-received

		// Closing the listener without cancelling Serve's context looks like
		// an unexpected accept failure, not a shutdown.
		require.NoError(t, srv.listener.Close())

		assert.Equal(t, "cancelled:slow", receive(t, reader))
		assert.Error(t, wait(), "Serve did not report the accept failure")
	})

	t.Run("cancels requests already received when the grace period expires", func(t *testing.T) {
		const requests = "slow\nnext\n"
		handler, received := newTestHandler()
		srv := newTestServer(t, 50*time.Millisecond, handler)
		// Guarantee both requests are in the server's buffer before the first
		// is handled. TCP doesn't promise how a write is split across reads.
		srv.listener = batchListener{Listener: srv.listener, size: len(requests)}
		shutdown, _ := serve(t, srv)
		conn, reader := dial(t, srv)
		send(t, conn, requests)
		<-received

		shutdown()

		assert.Equal(t, "cancelled:slow", receive(t, reader))
		assert.Equal(t, "cancelled:next", receive(t, reader))
		requireClosed(t, reader)
	})
}

func TestHandleConnection(t *testing.T) {
	t.Run("closes the connection without handling further requests when a response can't be written", func(t *testing.T) {
		handler, received := newTestHandler()
		srv := &Server{handler: handler, logger: slog.New(slog.DiscardHandler)}
		serverConn, client := net.Pipe()
		t.Cleanup(func() { client.Close() })
		require.NoError(t, client.SetDeadline(time.Now().Add(time.Second)))

		done := make(chan struct{})
		go func() {
			defer close(done)
			srv.handleConnection(context.Background(), failingWriter{serverConn})
		}()
		send(t, client, "first\nsecond\n")
		waitFor(t, done)

		assert.Equal(t, "first", <-received)
		assert.Empty(t, received, "handled a request after a write failed")
		requireClosed(t, bufio.NewReader(client))
	})
}

// newTestHandler returns a handler that holds requests named "slow" until
// they are cancelled and answers any other request at once, saying whether it
// was cancelled. The channel receives each request as the handler starts on
// it.
func newTestHandler() (Handler, chan string) {
	received := make(chan string, 10)
	handler := func(ctx context.Context, request string) string {
		received <- request
		if request == "slow" {
			<-ctx.Done()
		}
		if ctx.Err() != nil {
			return "cancelled:" + request
		}
		return "done:" + request
	}
	return handler, received
}

func newTestServer(t *testing.T, gracePeriod time.Duration, handler Handler) *Server {
	t.Helper()
	srv, err := New(Config{Addr: "127.0.0.1:0", GracePeriod: gracePeriod}, handler, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	return srv
}

// serve runs srv until shutdown is called or the test ends. wait waits for
// Serve to return and returns its error.
func serve(t *testing.T, srv *Server) (shutdown context.CancelFunc, wait func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var err error
	go func() {
		defer close(done)
		err = srv.Serve(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		waitFor(t, done)
	})

	wait = func() error {
		t.Helper()
		waitFor(t, done)
		return err
	}
	return cancel, wait
}

func dial(t *testing.T, srv *Server) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", srv.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	require.NoError(t, conn.SetDeadline(time.Now().Add(time.Second)))
	return conn, bufio.NewReader(conn)
}

func send(t *testing.T, conn net.Conn, data string) {
	t.Helper()
	_, err := io.WriteString(conn, data)
	require.NoError(t, err)
}

func receive(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	return strings.TrimSuffix(line, "\n")
}

// requireClosed fails the test unless the server closes the connection
// without sending anything more.
func requireClosed(t *testing.T, reader *bufio.Reader) {
	t.Helper()
	line, err := reader.ReadString('\n')
	require.Empty(t, line)
	var netErr net.Error
	require.False(t, errors.As(err, &netErr) && netErr.Timeout(), "connection still open")
	require.Error(t, err)
}

func waitFor(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out after 1s")
	}
}

// failingWriter is a connection whose writes always fail.
type failingWriter struct{ net.Conn }

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

// batchListener makes the first read on each accepted connection return
// exactly size bytes.
type batchListener struct {
	net.Listener
	size int
}

func (l batchListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &batchConn{Conn: conn, firstRead: l.size}, nil
}

type batchConn struct {
	net.Conn
	firstRead int
}

func (c *batchConn) Read(p []byte) (int, error) {
	if c.firstRead == 0 {
		return c.Conn.Read(p)
	}
	n := c.firstRead
	c.firstRead = 0
	return io.ReadFull(c.Conn, p[:n])
}
