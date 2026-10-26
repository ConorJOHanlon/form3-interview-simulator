package main

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePayment(t *testing.T) {
	tests := []struct {
		name           string
		request        string
		expectedAmount int
		expectedErr    error
	}{
		{name: "valid amount", request: "PAYMENT|10", expectedAmount: 10},
		{name: "large amount", request: "PAYMENT|20000", expectedAmount: 20000},

		{name: "empty request", request: "", expectedErr: errInvalidRequest},
		{name: "unknown message type", request: "INVALID|100", expectedErr: errInvalidRequest},
		{name: "lowercase message type", request: "payment|100", expectedErr: errInvalidRequest},
		{name: "missing separator", request: "PAYMENT100", expectedErr: errInvalidRequest},
		{name: "too many fields", request: "PAYMENT|100|extra", expectedErr: errInvalidRequest},

		{name: "empty amount", request: "PAYMENT|", expectedErr: errInvalidAmount},
		{name: "non-numeric amount", request: "PAYMENT|abc", expectedErr: errInvalidAmount},
		{name: "decimal amount", request: "PAYMENT|10.5", expectedErr: errInvalidAmount},
		{name: "amount with whitespace", request: "PAYMENT| 10", expectedErr: errInvalidAmount},
		{name: "amount overflowing int", request: "PAYMENT|99999999999999999999", expectedErr: errInvalidAmount},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			amount, err := parsePayment(tt.request)

			if tt.expectedErr != nil {
				require.ErrorIs(t, err, tt.expectedErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expectedAmount, amount)
		})
	}
}

func TestProcessingDelay(t *testing.T) {
	tests := []struct {
		amount        int
		expectedDelay time.Duration
	}{
		{amount: 1, expectedDelay: 0},
		{amount: 100, expectedDelay: 0},
		{amount: 101, expectedDelay: 101 * time.Millisecond},
		{amount: 9999, expectedDelay: 9999 * time.Millisecond},
		{amount: 10000, expectedDelay: 10 * time.Second},
		{amount: 10001, expectedDelay: 10 * time.Second},
		{amount: math.MaxInt, expectedDelay: 10 * time.Second},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expectedDelay, processingDelay(tt.amount), "amount %d", tt.amount)
	}
}
