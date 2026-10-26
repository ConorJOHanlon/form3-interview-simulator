package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// MaxRequestLength is the longest request line accepted, including its
// newline. A longer request can't be handled safely, so the connection is
// closed without a response.
const MaxRequestLength = 1024

var errIncompleteRequest = errors.New("connection closed before request was terminated")

// handleConnection serves requests from conn one at a time until the client
// disconnects or ctx is cancelled.
func (s *Server) handleConnection(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	logger := s.logger.With("remote_addr", conn.RemoteAddr())
	logger.Debug("connection opened")
	defer logger.Debug("connection closed")

	// Once ctx is cancelled, stop waiting for the next request. Only reads are
	// interrupted, so the response to an in-flight request can still be sent.
	stop := context.AfterFunc(ctx, func() { conn.SetReadDeadline(time.Now()) })
	defer stop()

	reader := bufio.NewReaderSize(conn, MaxRequestLength)
	for {
		request, err := readRequest(reader)
		if err != nil {
			switch {
			case ctx.Err() != nil:
				logger.Debug("closing connection after grace period")
			case errors.Is(err, bufio.ErrBufferFull):
				logger.Warn("request too long, closing connection", "max_length", MaxRequestLength)
			case !errors.Is(err, io.EOF):
				logger.Debug("failed to read request", "error", err)
			}
			return
		}

		response := s.handler(ctx, request)
		if _, err := fmt.Fprintf(conn, "%s\n", response); err != nil {
			logger.Debug("failed to write response", "error", err)
			return
		}
	}
}

// readRequest reads the next newline-terminated request, without its line
// terminator. A trailing "\r" is also removed so clients that send "\r\n"
// work too.
func readRequest(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadSlice('\n')
	if errors.Is(err, io.EOF) && len(line) > 0 {
		return "", errIncompleteRequest
	}
	if err != nil {
		return "", err
	}

	request := strings.TrimSuffix(string(line), "\n")
	return strings.TrimSuffix(request, "\r"), nil
}
