package server

import (
	"bufio"
	"context"
	"fmt"
	"net"
)

// handleConnection serves requests from conn one at a time until the client
// disconnects.
func (s *Server) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	logger := s.logger.With("remote_addr", conn.RemoteAddr())
	logger.Debug("connection opened")
	defer logger.Debug("connection closed")

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		response := s.handler(ctx, scanner.Text())
		if _, err := fmt.Fprintf(conn, "%s\n", response); err != nil {
			logger.Debug("failed to write response", "error", err)
			return
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Debug("failed to read request", "error", err)
	}
}
