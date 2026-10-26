package main

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	statusAccepted = "ACCEPTED"
	statusRejected = "REJECTED"
)

// response is a scheme reply, sent on the wire as "RESPONSE|<status>|<reason>".
type response struct {
	status string
	reason string
}

func (r response) String() string {
	return "RESPONSE|" + r.status + "|" + r.reason
}

var (
	responseAccepted       = response{status: statusAccepted, reason: "Transaction processed"}
	responseInvalidRequest = response{status: statusRejected, reason: "Invalid request"}
	responseInvalidAmount  = response{status: statusRejected, reason: "Invalid amount"}
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

func handleRequest(request string) response {
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
