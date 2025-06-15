package http

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/config"
	errors "github.com/drallgood/audiobookshelf-hardcover-sync/errors"
	"github.com/drallgood/audiobookshelf-hardcover-sync/logging"
	"github.com/hashicorp/go-retryablehttp"
)

// Client represents an HTTP client with retry, circuit breaker, and observability
type Client struct {
	*retryablehttp.Client
	config *config.Config
}

// NewClient creates a new HTTP client with the given configuration
func NewClient(cfg *config.Config) *Client {
	httpClient := &http.Client{
		Timeout: cfg.HTTP.Timeout,
		Transport: &http.Transport{
			MaxIdleConns:          cfg.HTTP.MaxIdleConns,
			MaxIdleConnsPerHost:   cfg.HTTP.MaxIdleConns,
			MaxConnsPerHost:       cfg.HTTP.MaxIdleConns * 2,
			IdleConnTimeout:       cfg.HTTP.IdleConnTimeout,
			DisableKeepAlives:     false,
			ForceAttemptHTTP2:     true,
			DisableCompression:    cfg.HTTP.DisableCompression,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 10 * time.Second,
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: cfg.HTTP.InsecureSkipVerify},
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
		},
	}

	retryClient := retryablehttp.NewClient()
	retryClient.HTTPClient = httpClient
	retryClient.RetryWaitMin = cfg.HTTP.RetryWaitMin
	retryClient.RetryWaitMax = cfg.HTTP.RetryWaitMax
	retryClient.RetryMax = int(cfg.HTTP.MaxRetries) // Use MaxRetries instead of RetryMax
	retryClient.CheckRetry = retryPolicy

	// Disable retryablehttp's default logger
	retryClient.Logger = nil

	return &Client{
		Client: retryClient,
		config: cfg,
	}
}

// retryPolicy determines whether to retry a request
func retryPolicy(ctx context.Context, resp *http.Response, err error) (bool, error) {
	// Don't retry on context cancellation or timeout
	if ctx.Err() != nil {
		return false, ctx.Err()
	}

	// Check for retryable errors
	if err != nil {
		// Check for network errors that might be temporary
		if netErr, ok := err.(net.Error); ok && netErr.Temporary() {
			return true, nil
		}

		// Check for connection reset errors
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return true, nil
		}

		// Check for DNS lookup errors
		if _, ok := err.(*net.DNSError); ok {
			return true, nil
		}

		// Check for TLS handshake errors
		if _, ok := err.(tls.RecordHeaderError); ok {
			return true, nil
		}

		// Don't retry on other errors
		return false, nil
	}

	// Retry on 5xx responses
	if resp != nil && resp.StatusCode >= 500 && resp.StatusCode <= 599 {
		return true, nil
	}

	// Don't retry on success or client errors
	return false, nil
}

// Do sends an HTTP request with the client's retry policy and logging
func (c *Client) Do(req *retryablehttp.Request) (*http.Response, error) {
	start := time.Now()

	// Log the request
	logging.Debug("sending HTTP request",
		"method", req.Method,
		"url", req.URL.String(),
	)

	// Execute the request
	resp, err := c.Client.Do(req)


	// Calculate request duration
	duration := time.Since(start)

	// Log the response or error
	if err != nil {
		logging.Error("HTTP request failed", err,
			"method", req.Method,
			"url", req.URL.String(),
			"duration", duration,
		)

		// Return the error with additional context
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}

	// Log successful responses
	logging.Debug("HTTP request completed",
		"method", req.Method,
		"url", req.URL.String(),
		"status", resp.StatusCode,
		"duration", duration,
	)

	// Check for rate limiting
	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfter := resp.Header.Get("Retry-After")
		if retryAfter != "" {
			logging.Warn("rate limited",
				"method", req.Method,
				"url", req.URL.String(),
				"retry_after", retryAfter,
			)
		}
	}

	return resp, nil
}

// Get performs a GET request
func (c *Client) Get(url string) (*http.Response, error) {
	req, err := retryablehttp.NewRequest("GET", url, nil)
	if err != nil {
		return nil, errors.NewWithCause(errors.APIError, err, "failed to create request")
	}
	return c.Do(req)
}

// Post performs a POST request with JSON body
func (c *Client) Post(url string, body interface{}) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, errors.NewWithCause(errors.ValidationError, err, "failed to marshal request body")
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := retryablehttp.NewRequest("POST", url, bodyReader)
	if err != nil {
		return nil, errors.NewWithCause(errors.APIError, err, "failed to create request")
	}
	req.Header.Set("Content-Type", "application/json")

	return c.Do(req)
}

// Put performs a PUT request with JSON body
func (c *Client) Put(url string, body interface{}) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, errors.NewWithCause(errors.ValidationError, err, "failed to marshal request body")
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := retryablehttp.NewRequest("PUT", url, bodyReader)
	if err != nil {
		return nil, errors.NewWithCause(errors.APIError, err, "failed to create request")
	}
	req.Header.Set("Content-Type", "application/json")

	return c.Do(req)
}

// Delete performs a DELETE request
func (c *Client) Delete(url string) (*http.Response, error) {
	req, err := retryablehttp.NewRequest("DELETE", url, nil)
	if err != nil {
		return nil, errors.NewWithCause(errors.APIError, err, "failed to create request")
	}
	return c.Do(req)
}

// NewRequest creates a new retryable request with context
func (c *Client) NewRequest(method, url string, body interface{}) (*retryablehttp.Request, error) {
	req, err := retryablehttp.NewRequest(method, url, body)
	if err != nil {
		return nil, errors.NewWithCause(errors.APIError, err, "failed to create request")
	}
	return req, nil
}
