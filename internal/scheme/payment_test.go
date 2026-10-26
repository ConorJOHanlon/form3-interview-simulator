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
	tests := map[string]struct {
		amount        int
		expectedDelay time.Duration
	}{
		"no delay for smallest amount":        {amount: 1, expectedDelay: 0},
		"no delay at threshold":               {amount: 100, expectedDelay: 0},
		"delay equals amount above threshold": {amount: 101, expectedDelay: 101 * time.Millisecond},
		"delay equals amount just below cap":  {amount: 9999, expectedDelay: 9999 * time.Millisecond},
		"delay is capped at cap":              {amount: 10000, expectedDelay: maxDelay},
		"delay is capped above cap":           {amount: 10001, expectedDelay: maxDelay},
		"delay is capped without overflow":    {amount: math.MaxInt, expectedDelay: maxDelay},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
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
}
