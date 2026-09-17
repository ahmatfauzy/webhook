package delivery

import (
	"math/rand"
	"net/http"
	"time"
)

// IsRetryable reports whether a failed attempt should be retried.
func IsRetryable(err error, httpStatus int, isTimeout bool) bool {
	if isTimeout || err != nil {
		// network error / timeout
		return true
	}
	switch httpStatus {
	case http.StatusRequestTimeout, // 408
		http.StatusConflict,            // 409
		http.StatusTooManyRequests,     // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout:      // 504
		return true
	default:
		return false
	}
}

func IsNonRetryable(httpStatus int) bool {
	switch httpStatus {
	case http.StatusBadRequest, // 400
		http.StatusUnauthorized,        // 401
		http.StatusForbidden,           // 403
		http.StatusNotFound,            // 404
		http.StatusMethodNotAllowed,    // 405
		http.StatusUnprocessableEntity: // 422
		return true
	default:
		return false
	}
}

// NextAttempt computes next_attempt_at from explicit schedule or exponential fallback with jitter.
func NextAttempt(schedule []time.Duration, attempt int, maxDelay time.Duration, jitter float64) time.Duration {
	var base time.Duration
	if attempt < len(schedule) {
		base = schedule[attempt]
	} else {
		// exponential fallback: 1s * 2^attempt
		base = time.Duration(1<<uint(attempt)) * time.Second
		if base > maxDelay {
			base = maxDelay
		}
	}
	if base > maxDelay {
		base = maxDelay
	}
	// jitter ± jitter*base
	if jitter > 0 {
		j := (rand.Float64()*2 - 1) * jitter // -jitter..+jitter
		base = time.Duration(float64(base) * (1 + j))
		if base < 0 {
			base = 0
		}
		if base > maxDelay {
			base = maxDelay
		}
	}
	return base
}
