// Package scheme simulates how the payment scheme handles requests.
package scheme

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	// Amounts up to this value are processed without delay.
	delayThreshold = 100
	maxDelay       = 10 * time.Second
)

var (
	errInvalidRequest = errors.New("invalid request")
	errInvalidAmount  = errors.New("invalid amount")
)

// Handle processes a single request and returns the response to send back.
// A payment still being processed when ctx is cancelled is rejected as
// cancelled. Handle has the signature of server.Handler.
func Handle(ctx context.Context, request string) string {
	amount, err := parsePayment(request)
	switch {
	case errors.Is(err, errInvalidRequest):
		return responseInvalidRequest.String()
	case errors.Is(err, errInvalidAmount):
		return responseInvalidAmount.String()
	}

	if !sleep(ctx, processingDelay(amount)) {
		return responseCancelled.String()
	}

	return responseAccepted.String()
}

// sleep waits for d and reports whether it did so before ctx was cancelled.
func sleep(ctx context.Context, d time.Duration) bool {
	// Checked first so a payment arriving after cancellation is never
	// accepted, even when it has no delay.
	if ctx.Err() != nil {
		return false
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// parsePayment parses a "PAYMENT|<amount>" request and returns the amount.
func parsePayment(request string) (int, error) {
	parts := strings.Split(request, "|")
	if len(parts) != 2 || parts[0] != "PAYMENT" {
		return 0, errInvalidRequest
	}

	// Atoi alone would also accept signed values such as "+5" and "-5".
	if strings.ContainsFunc(parts[1], isNotDigit) {
		return 0, errInvalidAmount
	}

	amount, err := strconv.Atoi(parts[1])
	if err != nil || amount < 1 {
		return 0, errInvalidAmount
	}

	return amount, nil
}

func isNotDigit(r rune) bool {
	return r < '0' || r > '9'
}

// processingDelay simulates counterparty processing time: one millisecond per
// unit of amount above the threshold, capped at maxDelay.
func processingDelay(amount int) time.Duration {
	if amount <= delayThreshold {
		return 0
	}
	// Compare before converting so very large amounts can't overflow Duration.
	if amount >= int(maxDelay/time.Millisecond) {
		return maxDelay
	}
	return time.Duration(amount) * time.Millisecond
}
