// Package retry classifies delivery outcomes as retryable or not and
// computes retry delays, per QUEUE_AND_DELIVERY_SEMANTICS.md's retry policy.
package retry

import (
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dhairyarajvarma/webhook-dispatcher/internal/delivery"
	"github.com/dhairyarajvarma/webhook-dispatcher/internal/store"
)

type Policy struct {
	MaxAttempts int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
}

// Classify maps a delivery result to a delivery_attempts outcome, per the
// retryable/non-retryable examples in QUEUE_AND_DELIVERY_SEMANTICS.md.
func Classify(result delivery.Result) string {
	if result.Success {
		return store.OutcomeSuccess
	}

	if result.HTTPStatus != nil {
		status := *result.HTTPStatus
		if status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || (status >= 500 && status < 600) {
			return store.OutcomeRetryableFailure
		}
		// Usually non-retryable: 400/401/403/404/422 and other 4xx classes.
		return store.OutcomeNonRetryableFailure
	}

	// No HTTP status means the request never got a response: network
	// failure, timeout, or malformed request (bad URL/scheme).
	if result.ErrorClass != nil && (*result.ErrorClass == "invalid_request" || *result.ErrorClass == "outbound_policy" || *result.ErrorClass == "signing_error") {
		return store.OutcomeNonRetryableFailure
	}
	return store.OutcomeRetryableFailure
}

// NextDelay returns a backoff duration for the given 1-based attempt number,
// using full jitter (a uniform random duration in [0, cappedDelay]) to avoid
// synchronized retry storms, capped at MaxBackoff.
//
// Delay growth saturates before doubling can overflow, including for very
// large attempt counts. Sampling also supports the maximum time.Duration.
func (p Policy) NextDelay(attemptNumber int) time.Duration {
	delay := p.delayCap(attemptNumber)
	if delay <= 0 {
		return 0
	}
	return time.Duration(rand.Uint64N(uint64(delay) + 1))
}

// NextDelayFor honors a bounded downstream hint without shortening local backoff.
func (p Policy) NextDelayFor(attemptNumber int, result delivery.Result) time.Duration {
	delay := p.NextDelay(attemptNumber)
	if result.HTTPStatus != nil && Classify(result) == store.OutcomeRetryableFailure {
		if hint := retryAfterDelay(result.RetryAfter, result.FinishedAt, p.MaxBackoff); hint > delay {
			delay = hint
		}
	}
	return delay
}

func retryAfterDelay(raw string, now time.Time, cap time.Duration) time.Duration {
	if len(raw) > 128 || cap <= 0 {
		return 0
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	digits := true
	for _, c := range raw {
		if c < '0' || c > '9' {
			digits = false
			break
		}
	}
	if digits {
		seconds, err := strconv.ParseUint(raw, 10, 64)
		if errors.Is(err, strconv.ErrRange) || seconds > uint64(cap/time.Second) {
			return cap
		}
		return time.Duration(seconds) * time.Second
	}
	date, err := http.ParseTime(raw)
	if err != nil || !date.After(now) {
		return 0
	}
	return min(date.Sub(now), cap)
}

func (p Policy) delayCap(attemptNumber int) time.Duration {
	delay := p.BaseBackoff
	if delay <= 0 || p.MaxBackoff <= 0 {
		return 0
	}
	if delay >= p.MaxBackoff {
		return p.MaxBackoff
	}
	for i := 1; i < attemptNumber && delay < p.MaxBackoff; i++ {
		if delay > p.MaxBackoff/2 {
			return p.MaxBackoff
		}
		delay *= 2
	}
	return delay
}
