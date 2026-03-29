package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/failsafe-go/failsafe-go"
	"github.com/failsafe-go/failsafe-go/circuitbreaker"
	"github.com/failsafe-go/failsafe-go/retrypolicy"
)

// HTTPError represents a non-retryable HTTP error (4xx except 429).
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}

// ResilientClient wraps an HTTP client with circuit breaker and retry policies.
// Each provider endpoint should have its own ResilientClient instance.
type ResilientClient struct {
	client *http.Client
	cb     circuitbreaker.CircuitBreaker[[]byte]
	retry  retrypolicy.RetryPolicy[[]byte]
}

const (
	defaultHTTPTimeout = 60 * time.Second
	cbFailureThreshold = 2
	cbCooldown         = 10 * time.Minute
	retryMaxRetries    = 3
	retryBackoffMax    = 30 * time.Second
)

// NewResilientClient creates a client with circuit breaker and retry.
func NewResilientClient(client *http.Client) *ResilientClient {
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}

	// Only count network errors and retryable HTTP errors (not 4xx client errors).
	isServiceError := func(_ []byte, err error) bool {
		if err == nil {
			return false
		}
		var httpErr *HTTPError
		return !errors.As(err, &httpErr)
	}

	cb := circuitbreaker.NewBuilder[[]byte]().
		HandleIf(isServiceError).
		WithFailureThreshold(cbFailureThreshold).
		WithDelay(cbCooldown).
		Build()

	retry := retrypolicy.NewBuilder[[]byte]().
		AbortIf(func(_ []byte, err error) bool {
			var httpErr *HTTPError
			if errors.As(err, &httpErr) {
				return true
			}
			return errors.Is(err, circuitbreaker.ErrOpen)
		}).
		WithMaxRetries(retryMaxRetries).
		WithBackoff(time.Second, retryBackoffMax).
		ReturnLastFailure().
		Build()

	return &ResilientClient{client: client, cb: cb, retry: retry}
}

// Do executes an HTTP request with retry and circuit breaker.
// The build function creates a fresh request for each attempt (enabling body re-reads on retry).
func (rc *ResilientClient) Do(ctx context.Context, build func() (*http.Request, error)) ([]byte, error) {
	return failsafe.With(rc.retry, rc.cb).
		WithContext(ctx).
		Get(func() ([]byte, error) {
			req, err := build()
			if err != nil {
				return nil, fmt.Errorf("build request: %w", err)
			}

			resp, err := rc.client.Do(req)
			if err != nil {
				return nil, err
			}
			defer func() { _ = resp.Body.Close() }()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, fmt.Errorf("read response: %w", err)
			}

			if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError {
				return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
			}
			if resp.StatusCode != http.StatusOK {
				return nil, &HTTPError{StatusCode: resp.StatusCode, Body: string(body)}
			}

			return body, nil
		})
}

// IsOpen returns true if the circuit breaker is in the open state.
func (rc *ResilientClient) IsOpen() bool {
	return rc.cb.IsOpen()
}
