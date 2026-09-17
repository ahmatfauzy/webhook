package delivery

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		status  int
		err     error
		timeout bool
		want    bool
	}{
		{http.StatusOK, nil, false, false},
		{http.StatusInternalServerError, nil, false, true},
		{http.StatusBadGateway, nil, false, true},
		{http.StatusServiceUnavailable, nil, false, true},
		{http.StatusGatewayTimeout, nil, false, true},
		{http.StatusTooManyRequests, nil, false, true},
		{http.StatusRequestTimeout, nil, false, true},
		{http.StatusConflict, nil, false, true},
		{http.StatusBadRequest, nil, false, false},
		{http.StatusUnauthorized, nil, false, false},
		{http.StatusForbidden, nil, false, false},
		{http.StatusNotFound, nil, false, false},
		{http.StatusUnprocessableEntity, nil, false, false},
		{0, errors.New("network"), false, true},
		{0, nil, true, true},
	}
	for _, tc := range tests {
		got := IsRetryable(tc.err, tc.status, tc.timeout)
		if got != tc.want {
			t.Errorf("IsRetryable(status=%d, err=%v, timeout=%v) = %v, want %v", tc.status, tc.err, tc.timeout, got, tc.want)
		}
	}
}

func TestNextAttemptSchedule(t *testing.T) {
	sched := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute}
	max := 6 * time.Hour
	// without jitter, should match schedule
	for i, want := range sched {
		got := NextAttempt(sched, i, max, 0)
		if got != want {
			t.Errorf("attempt %d got %v want %v", i, got, want)
		}
	}
	// beyond schedule falls back to exponential, capped
	got := NextAttempt(sched, 10, max, 0)
	if got > max {
		t.Fatalf("should cap at max, got %v", got)
	}
	// jitter should be within ±20% when set
	for i := 0; i < 10; i++ {
		got := NextAttempt(sched, 0, max, 0.2)
		if got < 24*time.Second || got > 36*time.Second {
			t.Fatalf("jitter out of range: %v", got)
		}
	}
}
