package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/form3tech-oss/interview-simulator/internal/scheme"
	"github.com/form3tech-oss/interview-simulator/internal/server"
	"github.com/form3tech-oss/interview-simulator/internal/server/servertest"
)

// tolerance is the allowed difference between a simulated delay and the
// observed response time.
const tolerance = 50 * time.Millisecond

// End-to-end tests: the real scheme handler served over TCP.
func TestSimulator(t *testing.T) {
	t.Run("responds to requests", func(t *testing.T) {
		tests := map[string]struct {
			request          string
			expectedResponse string
			minDuration      time.Duration
		}{
			"valid payment without delay": {
				request:          "PAYMENT|100",
				expectedResponse: "RESPONSE|ACCEPTED|Transaction processed",
			},
			"valid payment with delay": {
				request:          "PAYMENT|101",
				expectedResponse: "RESPONSE|ACCEPTED|Transaction processed",
				minDuration:      101 * time.Millisecond,
			},
			"valid payment with capped delay": {
				request:          "PAYMENT|20000",
				expectedResponse: "RESPONSE|ACCEPTED|Transaction processed",
				minDuration:      10 * time.Second,
			},
			"invalid request": {
				request:          "INVALID|100",
				expectedResponse: "RESPONSE|REJECTED|Invalid request",
			},
			"invalid amount": {
				request:          "PAYMENT|-5",
				expectedResponse: "RESPONSE|REJECTED|Invalid amount",
			},
		}

		for name, tt := range tests {
			t.Run(name, func(t *testing.T) {
				if testing.Short() && tt.minDuration > time.Second {
					t.Skip("slow test skipped in short mode")
				}
				t.Parallel()

				srv := servertest.Start(t, server.Config{}, scheme.Handle)
				client := servertest.Dial(t, srv.Addr)

				start := time.Now()
				client.Send(tt.request)
				response := client.Receive()
				elapsed := time.Since(start)

				assert.Equal(t, tt.expectedResponse, response)
				assert.GreaterOrEqual(t, elapsed, tt.minDuration, "response was sent too early")
				assert.LessOrEqual(t, elapsed, tt.minDuration+tolerance, "response was sent too late")
			})
		}
	})

	t.Run("processes payments on separate connections in parallel", func(t *testing.T) {
		const connections = 5
		srv := servertest.Start(t, server.Config{}, scheme.Handle)

		start := time.Now()
		clients := make([]*servertest.Client, connections)
		for i := range clients {
			clients[i] = servertest.Dial(t, srv.Addr)
			clients[i].Send("PAYMENT|500")
		}
		for _, client := range clients {
			assert.Equal(t, "RESPONSE|ACCEPTED|Transaction processed", client.Receive())
		}

		assert.Less(t, time.Since(start), 500*time.Millisecond+tolerance)
	})
}
