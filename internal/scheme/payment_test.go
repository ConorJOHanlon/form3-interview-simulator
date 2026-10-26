package scheme

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePayment(t *testing.T) {
	t.Run("returns the amount of a valid request", func(t *testing.T) {
		tests := map[string]struct {
			request        string
			expectedAmount int
		}{
			"small amount":     {request: "PAYMENT|10", expectedAmount: 10},
			"amount above cap": {request: "PAYMENT|20000", expectedAmount: 20000},
			"leading zeros":    {request: "PAYMENT|007", expectedAmount: 7},
		}

		for name, tt := range tests {
			t.Run(name, func(t *testing.T) {
				amount, err := parsePayment(tt.request)

				require.NoError(t, err)
				assert.Equal(t, tt.expectedAmount, amount)
			})
		}
	})

	t.Run("rejects a malformed request", func(t *testing.T) {
		tests := map[string]string{
			"empty request":          "",
			"unknown message type":   "INVALID|100",
			"lowercase message type": "payment|100",
			"missing separator":      "PAYMENT100",
			"too many fields":        "PAYMENT|100|extra",
		}

		for name, request := range tests {
			t.Run(name, func(t *testing.T) {
				_, err := parsePayment(request)

				assert.ErrorIs(t, err, errInvalidRequest)
			})
		}
	})

	t.Run("rejects an invalid amount", func(t *testing.T) {
		tests := map[string]string{
			"empty amount":           "PAYMENT|",
			"non-numeric amount":     "PAYMENT|abc",
			"decimal amount":         "PAYMENT|10.5",
			"amount with whitespace": "PAYMENT| 10",
			"amount overflowing int": "PAYMENT|99999999999999999999",
			"zero amount":            "PAYMENT|0",
			"all zeros amount":       "PAYMENT|000",
			"negative amount":        "PAYMENT|-5",
			"explicitly signed":      "PAYMENT|+5",
		}

		for name, request := range tests {
			t.Run(name, func(t *testing.T) {
				_, err := parsePayment(request)

				assert.ErrorIs(t, err, errInvalidAmount)
			})
		}
	})
}

func TestProcessingDelay(t *testing.T) {
	// Ordered by amount so the boundaries read in sequence.
	tests := []struct {
		name          string
		amount        int
		expectedDelay time.Duration
	}{
		{name: "no delay for smallest amount", amount: 1, expectedDelay: 0},
		{name: "no delay at threshold", amount: 100, expectedDelay: 0},
		{name: "delay equals amount above threshold", amount: 101, expectedDelay: 101 * time.Millisecond},
		{name: "delay equals amount just below cap", amount: 9999, expectedDelay: 9999 * time.Millisecond},
		{name: "delay is capped at cap", amount: 10000, expectedDelay: maxDelay},
		{name: "delay is capped above cap", amount: 10001, expectedDelay: maxDelay},
		{name: "delay is capped without overflow", amount: math.MaxInt, expectedDelay: maxDelay},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedDelay, processingDelay(tt.amount))
		})
	}
}

func TestHandle(t *testing.T) {
	tests := map[string]struct {
		request          string
		expectedResponse string
	}{
		"accepts a valid payment":     {request: "PAYMENT|10", expectedResponse: "RESPONSE|ACCEPTED|Transaction processed"},
		"rejects a malformed request": {request: "INVALID|10", expectedResponse: "RESPONSE|REJECTED|Invalid request"},
		"rejects an invalid amount":   {request: "PAYMENT|abc", expectedResponse: "RESPONSE|REJECTED|Invalid amount"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.expectedResponse, Handle(context.Background(), tt.request))
		})
	}

	t.Run("cancels a payment that is still processing when the context is cancelled", func(t *testing.T) {
		const cancelAfter = 50 * time.Millisecond
		ctx, cancel := context.WithTimeout(context.Background(), cancelAfter)
		defer cancel()

		start := time.Now()
		response := Handle(ctx, "PAYMENT|5000")
		elapsed := time.Since(start)

		assert.Equal(t, "RESPONSE|REJECTED|Cancelled", response)
		assert.GreaterOrEqual(t, elapsed, cancelAfter)
		assert.Less(t, elapsed, cancelAfter+50*time.Millisecond, "processing was not interrupted")
	})

	t.Run("cancels a payment received after the context is cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		for _, request := range []string{"PAYMENT|10", "PAYMENT|5000"} {
			assert.Equal(t, "RESPONSE|REJECTED|Cancelled", Handle(ctx, request), request)
		}
	})

	t.Run("still rejects invalid requests after the context is cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		assert.Equal(t, "RESPONSE|REJECTED|Invalid amount", Handle(ctx, "PAYMENT|0"))
	})
}
