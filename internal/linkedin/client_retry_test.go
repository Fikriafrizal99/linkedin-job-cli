package linkedin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"linkedin-jobs/internal/config"
)

func retryTestClient(maxAttempts int) *Client {
	return New(config.Config{
		UserAgent:             "test",
		RequestTimeoutSeconds: 2,
		HTTPMaxAttempts:       maxAttempts,
		HTTPRetryBaseSeconds:  0,
	})
}

func TestGetRetries429AndHonorsRetryAfter(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, "slow down")
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	body, _, status, err := retryTestClient(3).get(srv.URL, false, nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if status != http.StatusOK || body != "ok" {
		t.Fatalf("status=%d body=%q", status, body)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("calls=%d want 3", got)
	}
}

func TestGetRetriesTransient5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	body, _, status, err := retryTestClient(3).get(srv.URL, false, nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if status != http.StatusOK || body != "ok" {
		t.Fatalf("status=%d body=%q", status, body)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("calls=%d want 2", got)
	}
}

func TestGetDoesNotRetry403(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "blocked")
	}))
	defer srv.Close()

	body, _, status, err := retryTestClient(3).get(srv.URL, false, nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if status != http.StatusForbidden || body != "blocked" {
		t.Fatalf("status=%d body=%q", status, body)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("403 retried: calls=%d", got)
	}
}

func TestRetryAfterSecondsClamp(t *testing.T) {
	if got, ok := retryAfterSeconds("120"); !ok || got != 60 {
		t.Fatalf("got=%v ok=%v want 60,true", got, ok)
	}
	if _, ok := retryAfterSeconds("nonsense"); ok {
		t.Fatal("invalid Retry-After should not parse")
	}
}
