// Package server implements a TCP server for line-based request/response
// protocols. Each connection carries newline-terminated requests that are
// handled one at a time. The protocol itself is supplied as a Handler.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
)

// Handler returns the response to a single request. Neither the request nor
// the response include the line terminator.
type Handler func(ctx context.Context, request string) string

// Config configures a Server.
type Config struct {
	// Addr is the TCP address to listen on, for example ":8080".
	Addr string
}

// Server accepts connections and passes each request to its Handler.
type Server struct {
	listener net.Listener
	handler  Handler
	logger   *slog.Logger
}

// New returns a Server listening on cfg.Addr. Listening here rather than in
// Serve reports errors such as the address being in use before serving
// starts, and lets callers learn the address when listening on port 0.
func New(cfg Config, handler Handler, logger *slog.Logger) (*Server, error) {
	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %q: %w", cfg.Addr, err)
	}

	return &Server{listener: listener, handler: handler, logger: logger}, nil
}

// Addr returns the address the server is listening on.
func (s *Server) Addr() net.Addr {
	return s.listener.Addr()
}

// Serve accepts connections until ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
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

		go s.handleConnection(ctx, conn)
	}
}
