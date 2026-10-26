package main

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	responseAccepted       = "RESPONSE|ACCEPTED|Transaction processed"
	responseInvalidRequest = "RESPONSE|REJECTED|Invalid request"
	responseInvalidAmount  = "RESPONSE|REJECTED|Invalid amount"
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

func handleRequest(request string) string {
	amount, err := parsePayment(request)
	switch {
	case errors.Is(err, errInvalidRequest):
		return responseInvalidRequest
	case errors.Is(err, errInvalidAmount):
		return responseInvalidAmount
	}

	time.Sleep(processingDelay(amount))

	return responseAccepted
}

// parsePayment parses a "PAYMENT|<amount>" request and returns the amount.
func parsePayment(request string) (int, error) {
	parts := strings.Split(request, "|")
	if len(parts) != 2 || parts[0] != "PAYMENT" {
		return 0, errInvalidRequest
	}

	amount, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, errInvalidAmount
	}

	return amount, nil
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
