// Package server implements a TCP server for line-based request/response
// protocols. Each connection carries newline-terminated requests that are
// handled one at a time. The protocol itself is supplied as a Handler.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"
)

// Handler returns the response to a single request. Neither the request nor
// the response include the line terminator.
//
// ctx is cancelled when the shutdown grace period expires. The handler should
// then return promptly; its response is still sent to the client.
type Handler func(ctx context.Context, request string) string

// Config configures a Server.
type Config struct {
	// Addr is the TCP address to listen on, for example ":8080".
	Addr string
	// GracePeriod is how long existing connections keep being served after
	// shutdown starts, before in-flight requests are cancelled.
	GracePeriod time.Duration
}

// Server accepts connections and passes each request to its Handler.
type Server struct {
	listener    net.Listener
	handler     Handler
	gracePeriod time.Duration
	logger      *slog.Logger
	conns       sync.WaitGroup
}

// New returns a Server listening on cfg.Addr. Listening here rather than in
// Serve reports errors such as the address being in use before serving
// starts, and lets callers learn the address when listening on port 0.
func New(cfg Config, handler Handler, logger *slog.Logger) (*Server, error) {
	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %q: %w", cfg.Addr, err)
	}

	return &Server{
		listener:    listener,
		handler:     handler,
		gracePeriod: cfg.GracePeriod,
		logger:      logger,
	}, nil
}

// Addr returns the address the server is listening on.
func (s *Server) Addr() net.Addr {
	return s.listener.Addr()
}

// Serve accepts connections until ctx is cancelled, then shuts down
// gracefully:
//
//  1. New connections are refused, but existing connections keep being
//     served, including new requests sent on them.
//  2. If connections are still open when the grace period expires, the
//     context passed to in-flight handlers is cancelled. Each connection is
//     closed once its current response, if any, has been written.
//  3. Serve returns once every connection has closed.
//
// Serve must only be called once.
func (s *Server) Serve(ctx context.Context) error {
	// Requests are cancelled by the grace period expiring, not by ctx.
	requestCtx, cancelRequests := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelRequests()

	err := s.acceptConnections(ctx, requestCtx)
	if err != nil {
		// The server can't carry on, so don't wait out the grace period.
		cancelRequests()
	}
	s.drain(cancelRequests)

	return err
}

// acceptConnections serves each new connection in its own goroutine until ctx
// is cancelled.
func (s *Server) acceptConnections(ctx, requestCtx context.Context) error {
	defer s.listener.Close()
	stop := context.AfterFunc(ctx, func() { s.listener.Close() })
	defer stop()

	s.logger.Info("listening", "addr", s.Addr())
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept connection: %w", err)
		}

		s.conns.Go(func() { s.handleConnection(requestCtx, conn) })
	}
}

// drain waits for every connection to close, cancelling in-flight requests if
// any are still open when the grace period expires.
func (s *Server) drain(cancelRequests context.CancelFunc) {
	s.logger.Info("shutting down", "grace_period", s.gracePeriod)

	closed := make(chan struct{})
	go func() {
		s.conns.Wait()
		close(closed)
	}()

	select {
	case <-closed:
	case <-time.After(s.gracePeriod):
		s.logger.Info("grace period expired, cancelling in-flight requests")
		cancelRequests()
		<-closed
	}

	s.logger.Info("shutdown complete")
}
