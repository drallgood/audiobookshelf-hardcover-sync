package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"sync"
	"time"
)

// Response represents an HTTP response
type Response struct {
	StatusCode int
	Body      []byte
	Header    http.Header
}

// BatchClientConfig holds configuration for the batch HTTP client
type BatchClientConfig struct {
	BatchSize  int
	MaxWorkers int
	RateLimit  time.Duration
}

// BatchRequest represents a single request in a batch
type BatchRequest struct {
	Method  string
	URL     string
	Body    interface{}
	Headers map[string]string
}

// BatchResponse represents the response for a single batched request
type BatchResponse struct {
	Request  *BatchRequest
	Response *Response
	Error    error
}

// BatchClient handles batch HTTP requests
type BatchClient struct {
	client     *Client
	config     BatchClientConfig
	maxRetries int
}

// NewBatchClient creates a new batch client with the given configuration
func NewBatchClient(client *Client, cfg BatchClientConfig) *BatchClient {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 10
	}
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = 5
	}
	if cfg.RateLimit <= 0 {
		cfg.RateLimit = 100 * time.Millisecond // 10 req/s by default
	}

	return &BatchClient{
		client:     client,
		config:     cfg,
		maxRetries: 3,
	}
}

// WithMaxRetries sets the maximum number of retries for failed requests
func (bc *BatchClient) WithMaxRetries(retries int) *BatchClient {
	bc.maxRetries = retries
	return bc
}

// WithBatchSize sets the maximum number of requests per batch
func (bc *BatchClient) WithBatchSize(size int) *BatchClient {
	bc.config.BatchSize = size
	return bc
}

// WithWorkers sets the number of concurrent workers
func (bc *BatchClient) WithWorkers(workers int) *BatchClient {
	bc.config.MaxWorkers = workers
	return bc
}

// WithRateLimit sets the minimum time between batch executions
func (bc *BatchClient) WithRateLimit(limit time.Duration) *BatchClient {
	bc.config.RateLimit = limit
	return bc
}

// DoBatch executes multiple HTTP requests in parallel with controlled concurrency
func (bc *BatchClient) DoBatch(ctx context.Context, requests []*BatchRequest) []*BatchResponse {
	if len(requests) == 0 {
		return nil
	}

	// Create channels for work distribution and results
	workChan := make(chan *BatchRequest, len(requests))
	resultChan := make(chan *BatchResponse, len(requests))
	var wg sync.WaitGroup

	// Start worker pool
	for i := 0; i < bc.config.MaxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for req := range workChan {
				resp, err := bc.doRequest(ctx, req)
				resultChan <- &BatchResponse{
					Request:  req,
					Response: resp,
					Error:    err,
				}

				// Respect rate limiting
				time.Sleep(bc.config.RateLimit)
			}
		}()
	}

	// Send work to workers
	go func() {
		for _, req := range requests {
			workChan <- req
		}
		close(workChan)
	}()

	// Wait for all workers to finish
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results
	var results []*BatchResponse
	for result := range resultChan {
		results = append(results, result)
	}

	return results
}

// doRequest executes a single request with the appropriate method
func (bc *BatchClient) doRequest(ctx context.Context, br *BatchRequest) (*Response, error) {
	req, err := bc.client.NewRequest(br.Method, br.URL, br.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	for k, v := range br.Headers {
		req.Header.Set(k, v)
	}

	// Execute request
	resp, err := bc.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return &Response{
		StatusCode: resp.StatusCode,
		Body:      body,
		Header:    resp.Header,
	}, nil
}

// shouldRetry determines if a request should be retried based on the error
func shouldRetry(err error) bool {
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return true
	}
	return false
}

// backoff calculates the backoff duration with jitter
func backoff(attempt int, min, max time.Duration) time.Duration {
	duration := min * time.Duration(1<<uint(attempt))
	if duration > max {
		duration = max
	}
	// Add jitter
	jitter := time.Duration(rand.Int63n(int64(duration / 2)))
	return duration/2 + jitter
}

// BatchGraphQLRequest represents a single GraphQL request in a batch
type BatchGraphQLRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables,omitempty"`
}

// BatchGraphQLResponse represents the response for a batched GraphQL request
type BatchGraphQLResponse struct {
	Data   json.RawMessage        `json:"data,omitempty"`
	Errors []*GraphQLError       `json:"errors,omitempty"`
	Extras map[string]interface{} `json:"extensions,omitempty"`
}

// DoGraphQLBatch executes multiple GraphQL queries in a single request
func (bc *BatchClient) DoGraphQLBatch(ctx context.Context, endpoint string, requests []*BatchGraphQLRequest) ([]*BatchGraphQLResponse, error) {
	if len(requests) == 0 {
		return nil, fmt.Errorf("no requests provided")
	}

	// Convert requests to JSON
	body, err := json.Marshal(requests)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal requests: %w", err)
	}

	// Create batch request
	req := &BatchRequest{
		Method:  "POST",
		URL:     endpoint,
		Body:    body,
		Headers: map[string]string{"Content-Type": "application/json"},
	}

	// Execute request
	resp, err := bc.doRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("graphql batch request failed: %w", err)
	}

	// Parse response
	var responses []*BatchGraphQLResponse
	if err := json.Unmarshal(resp.Body, &responses); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return responses, nil
}

// GraphQLError represents an error in a GraphQL response
type GraphQLError struct {
	Message    string                 `json:"message"`
	Path       []string               `json:"path,omitempty"`
	Locations  []Location             `json:"locations,omitempty"`
	Extensions map[string]interface{} `json:"extensions,omitempty"`
}

// Location represents a location in a GraphQL document
type Location struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}
