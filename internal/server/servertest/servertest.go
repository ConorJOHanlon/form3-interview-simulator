// Package servertest provides helpers for testing servers built with package
// server, in the style of net/http/httptest.
package servertest

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/form3tech-oss/interview-simulator/internal/server"
)

const (
	// readTimeout bounds every read so a server bug fails the test instead of
	// hanging it. It exceeds the longest delay any handler under test uses.
	readTimeout = 15 * time.Second
	// stopTimeout bounds how long a test waits for the server to stop when
	// the test ends.
	stopTimeout = 15 * time.Second
)

// Server is a running server under test.
type Server struct {
	// Addr is the address clients can connect to.
	Addr string

	shutdown context.CancelFunc
	stopped  chan struct{}
	err      error
}

// Start serves handler on a random loopback port. cfg.Addr is ignored. The
// server is shut down, and must stop, before the test ends. Register clients
// after starting the server so they are closed first, letting it stop without
// waiting out its grace period.
func Start(t testing.TB, cfg server.Config, handler server.Handler) *Server {
	t.Helper()

	cfg.Addr = "127.0.0.1:0"
	// Server logs appear in the test output, shown when a test fails or with
	// -v. Serve waits for every connection to close, so nothing logs after
	// the test ends.
	logger := slog.New(slog.NewTextHandler(t.Output(), &slog.HandlerOptions{Level: slog.LevelDebug}))
	srv, err := server.New(cfg, handler, logger)
	if err != nil {
		t.Fatalf("start server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{Addr: srv.Addr().String(), shutdown: cancel, stopped: make(chan struct{})}
	go func() {
		defer close(s.stopped)
		s.err = srv.Serve(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		select {
		case <-s.stopped:
			if s.err != nil {
				t.Errorf("serve: %v", s.err)
			}
		case <-time.After(stopTimeout):
			t.Errorf("server did not stop within %v", stopTimeout)
		}
	})

	return s
}

// Shutdown tells the server to shut down, without waiting for it to stop.
func (s *Server) Shutdown() {
	s.shutdown()
}

// Stopped returns a channel that is closed once the server has stopped.
func (s *Server) Stopped() <-chan struct{} {
	return s.stopped
}

// Err returns the error the server stopped with. It is only valid once
// Stopped is closed.
func (s *Server) Err() error {
	return s.err
}

// Client is a connection to a server under test. Its methods fail the test on
// unexpected errors, so they must be called from the test goroutine.
type Client struct {
	t      testing.TB
	conn   *net.TCPConn
	reader *bufio.Reader
}

// Dial connects to addr. The connection is closed when the test ends.
func Dial(t testing.TB, addr string) *Client {
	t.Helper()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { conn.Close() })

	return &Client{t: t, conn: conn.(*net.TCPConn), reader: bufio.NewReader(conn)}
}

// Send writes request followed by a newline.
func (c *Client) Send(request string) {
	c.t.Helper()
	c.SendRaw(request + "\n")
}

// SendRaw writes data exactly as given.
func (c *Client) SendRaw(data string) {
	c.t.Helper()
	if _, err := c.conn.Write([]byte(data)); err != nil {
		c.t.Fatalf("send %q: %v", data, err)
	}
}

// Receive reads the next response line, without its newline.
func (c *Client) Receive() string {
	c.t.Helper()
	line, err := c.readLine()
	if err != nil {
		c.t.Fatalf("receive response: %v", err)
	}
	return strings.TrimSuffix(line, "\n")
}

// RequireClosed fails the test unless the server closes the connection
// without sending anything more.
func (c *Client) RequireClosed() {
	c.t.Helper()
	line, err := c.readLine()
	switch {
	case err == nil || line != "":
		c.t.Fatalf("expected connection to be closed, got %q", line)
	case errors.Is(err, net.ErrClosed):
		c.t.Fatalf("client connection was closed locally")
	case isTimeout(err):
		c.t.Fatalf("connection still open after %v", readTimeout)
	}
	// Any other error, normally io.EOF or a connection reset, means the
	// server closed the connection.
}

// CloseWrite closes the sending side of the connection, so the server reads
// end of file while the client can still receive.
func (c *Client) CloseWrite() {
	c.t.Helper()
	if err := c.conn.CloseWrite(); err != nil {
		c.t.Fatalf("close write: %v", err)
	}
}

// Close closes the connection.
func (c *Client) Close() {
	c.conn.Close()
}

func (c *Client) readLine() (string, error) {
	if err := c.conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		return "", err
	}
	return c.reader.ReadString('\n')
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
