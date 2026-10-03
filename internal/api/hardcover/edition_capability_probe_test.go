package hardcover

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/stretchr/testify/require"
)

func TestProbeInsertEditionCapabilityClassifiesMutationResponses(t *testing.T) {
	const validationBody = `{"errors":[{"message":"missing required field 'book_id'","extensions":{"path":"$.selectionSet.insert_edition.args.book_id","code":"validation-failed"}}]}`
	tests := []struct {
		name   string
		status int
		body   string
		want   EditionCapabilityProbeState
	}{
		{
			name:   "exact missing book_id validation proves scope",
			status: http.StatusOK,
			body:   validationBody,
			want:   EditionCapabilityProbeAllowed,
		},
		{
			name:   "exact validation response with null data proves scope",
			status: http.StatusOK,
			body:   `{"errors":[{"message":"missing required field 'book_id'","extensions":{"path":"$.selectionSet.insert_edition.args.book_id","code":"validation-failed"}}],"data":null}`,
			want:   EditionCapabilityProbeAllowed,
		},
		{
			name:   "matching validation body with HTTP 201 is unverified",
			status: http.StatusCreated,
			body:   validationBody,
			want:   EditionCapabilityProbeUnverified,
		},
		{
			name:   "matching validation body with HTTP 202 is unverified",
			status: http.StatusAccepted,
			body:   validationBody,
			want:   EditionCapabilityProbeUnverified,
		},
		{
			name:   "matching validation body with HTTP 206 is unverified",
			status: http.StatusPartialContent,
			body:   validationBody,
			want:   EditionCapabilityProbeUnverified,
		},
		{
			name:   "wrong validation message is unverified",
			status: http.StatusOK,
			body:   `{"errors":[{"message":"missing required field 'edition'","extensions":{"path":"$.selectionSet.insert_edition.args.book_id","code":"validation-failed"}}]}`,
			want:   EditionCapabilityProbeUnverified,
		},
		{
			name:   "wrong validation path is unverified",
			status: http.StatusOK,
			body:   `{"errors":[{"message":"missing required field 'book_id'","extensions":{"path":"$.other.path","code":"validation-failed"}}]}`,
			want:   EditionCapabilityProbeUnverified,
		},
		{
			name:   "validation response with data is unverified",
			status: http.StatusOK,
			body:   `{"errors":[{"message":"missing required field 'book_id'","extensions":{"path":"$.selectionSet.insert_edition.args.book_id","code":"validation-failed"}}],"data":{"insert_edition":null}}`,
			want:   EditionCapabilityProbeUnverified,
		},
		{
			name:   "insufficient scope denies operation",
			status: http.StatusForbidden,
			body:   `{"error":"insufficient_scope","error_description":"Missing scopes: write:catalog:append","scope":"write:catalog:append"}`,
			want:   EditionCapabilityProbeDenied,
		},
		{
			name:   "unauthorized response is unverified",
			status: http.StatusUnauthorized,
			body:   `{"error":"invalid_token"}`,
			want:   EditionCapabilityProbeUnverified,
		},
		{
			name:   "rate limit is unverified",
			status: http.StatusTooManyRequests,
			body:   `{"error":"rate_limited"}`,
			want:   EditionCapabilityProbeUnverified,
		},
		{
			name:   "server failure is unverified",
			status: http.StatusServiceUnavailable,
			body:   `{"error":"unavailable"}`,
			want:   EditionCapabilityProbeUnverified,
		},
		{
			name:   "unexpected successful mutation response is unverified",
			status: http.StatusOK,
			body:   `{"data":{"insert_edition":{"id":99,"errors":[]}}}`,
			want:   EditionCapabilityProbeUnverified,
		},
		{
			name:   "unexpected GraphQL error is unverified",
			status: http.StatusOK,
			body:   `{"errors":[{"message":"book lookup temporarily failed"}],"data":null}`,
			want:   EditionCapabilityProbeUnverified,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				var payload struct {
					Query string `json:"query"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				require.Equal(t, insertEditionCapabilityProbeMutation, payload.Query)
				require.NotContains(t, payload.Query, "book_id:")
				require.NotContains(t, payload.Query, "edition:")
				require.NotContains(t, payload.Query, "external_id")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			client := CreateTestClient(server)
			got := client.ProbeInsertEditionCapability(context.Background())

			require.Equal(t, test.want, got)
			require.Equal(t, int32(1), requests.Load(), "probe sends one argument-free mutation")
			require.Equal(t, uint64(1), client.rateLimiter.GetMetrics().Requests, "probe uses the configured Hardcover rate limiter")
		})
	}
}

type capabilityProbeTimeoutTransport struct {
	requests      atomic.Int32
	contextErrors chan error
}

func (rt *capabilityProbeTimeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.requests.Add(1)
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-req.Context().Done():
		rt.contextErrors <- req.Context().Err()
		return nil, req.Context().Err()
	case <-timer.C:
		rt.contextErrors <- nil
		return nil, errors.New("request context was not canceled before the bounded fallback")
	}
}

func TestCapabilityProbeTimeoutIsUnverified(t *testing.T) {
	logger.Setup(logger.Config{Level: "error", Format: "json"})
	tests := []struct {
		name  string
		probe func(*Client) EditionCapabilityProbeState
	}{
		{name: "insert edition", probe: func(client *Client) EditionCapabilityProbeState {
			return client.ProbeInsertEditionCapability(context.Background())
		}},
		{name: "upsert book", probe: func(client *Client) EditionCapabilityProbeState {
			return client.ProbeUpsertBookCapability(context.Background())
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				config := DefaultClientConfig()
				config.BaseURL = "https://hardcover.example.test/graphql"
				config.Timeout = 100 * time.Millisecond
				config.MaxRetries = 0
				config.RateLimit = time.Nanosecond
				config.MaxConcurrent = 1
				client := NewClientWithConfig(config, "test-token", logger.Get())
				transport := &capabilityProbeTimeoutTransport{contextErrors: make(chan error, 1)}
				client.httpClient.Transport = transport

				got := test.probe(client)

				require.Equal(t, EditionCapabilityProbeUnverified, got)
				require.Equal(t, int32(1), transport.requests.Load(), "a timed-out probe must not be retried")
				require.Equal(t, uint64(1), client.rateLimiter.GetMetrics().Requests)
				require.ErrorIs(t, <-transport.contextErrors, context.DeadlineExceeded,
					"the configured timeout must cancel the in-flight HTTP request")
			})
		})
	}
}

func TestProbeInsertEditionCapabilityDryRunSkipsMutation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	client := CreateTestClient(server)
	client.dryRun = true

	got := client.ProbeInsertEditionCapability(context.Background())

	require.Equal(t, EditionCapabilityProbeAllowed, got)
	require.Zero(t, requests.Load())
}

func TestProbeUpsertBookCapabilityClassifiesValidationAndScopeResponses(t *testing.T) {
	const validationBody = `{"errors":[{"message":"missing required field 'book'","extensions":{"path":"$.selectionSet.upsert_book.args.book","code":"validation-failed"}}],"data":null}`
	tests := []struct {
		name   string
		status int
		body   string
		want   EditionCapabilityProbeState
	}{
		{
			name:   "exact required book validation proves scope",
			status: http.StatusOK,
			body:   validationBody,
			want:   EditionCapabilityProbeAllowed,
		},
		{
			name:   "matching validation body with HTTP 201 is unverified",
			status: http.StatusCreated,
			body:   validationBody,
			want:   EditionCapabilityProbeUnverified,
		},
		{
			name:   "matching validation body with HTTP 202 is unverified",
			status: http.StatusAccepted,
			body:   validationBody,
			want:   EditionCapabilityProbeUnverified,
		},
		{
			name:   "matching validation body with HTTP 206 is unverified",
			status: http.StatusPartialContent,
			body:   validationBody,
			want:   EditionCapabilityProbeUnverified,
		},
		{
			name:   "field not found is not scope evidence",
			status: http.StatusOK,
			body:   `{"errors":[{"message":"field 'upsert_book' not found in type: 'mutation_root'","extensions":{"code":"validation-failed"}}],"data":null}`,
			want:   EditionCapabilityProbeUnverified,
		},
		{
			name:   "near match message is unverified",
			status: http.StatusOK,
			body:   `{"errors":[{"message":"missing required field 'edition'","extensions":{"path":"$.selectionSet.upsert_book.args.book","code":"validation-failed"}}],"data":null}`,
			want:   EditionCapabilityProbeUnverified,
		},
		{
			name:   "near match path is unverified",
			status: http.StatusOK,
			body:   `{"errors":[{"message":"missing required field 'book'","extensions":{"path":"$.other.path","code":"validation-failed"}}],"data":null}`,
			want:   EditionCapabilityProbeUnverified,
		},
		{
			name:   "validation response with data is unverified",
			status: http.StatusOK,
			body:   `{"errors":[{"message":"missing required field 'book'","extensions":{"path":"$.selectionSet.upsert_book.args.book","code":"validation-failed"}}],"data":{"upsert_book":null}}`,
			want:   EditionCapabilityProbeUnverified,
		},
		{
			name:   "insufficient scope denies operation",
			status: http.StatusForbidden,
			body:   `{"error":"insufficient_scope","error_description":"Missing scopes: write:catalog:append","scope":"write:catalog:append"}`,
			want:   EditionCapabilityProbeDenied,
		},
		{
			name:   "unauthorized response is unverified",
			status: http.StatusUnauthorized,
			body:   `{"error":"invalid_token"}`,
			want:   EditionCapabilityProbeUnverified,
		},
		{
			name:   "rate limit is unverified",
			status: http.StatusTooManyRequests,
			body:   `{"error":"rate_limited"}`,
			want:   EditionCapabilityProbeUnverified,
		},
		{
			name:   "server failure is unverified",
			status: http.StatusServiceUnavailable,
			body:   `{"error":"unavailable"}`,
			want:   EditionCapabilityProbeUnverified,
		},
		{
			name:   "unexpected mutation success is unverified",
			status: http.StatusOK,
			body:   `{"data":{"upsert_book":{"id":99}}}`,
			want:   EditionCapabilityProbeUnverified,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				var payload struct {
					Query string `json:"query"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				require.Equal(t, upsertBookCapabilityProbeMutation, payload.Query)
				require.NotContains(t, payload.Query, "book:", "the probe must omit the required book argument")
				require.NotContains(t, payload.Query, "external_id", "the probe must not submit a catalogue identifier")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			client := CreateTestClient(server)
			got := client.ProbeUpsertBookCapability(context.Background())

			require.Equal(t, test.want, got)
			require.Equal(t, int32(1), requests.Load(), "probe sends one validation-only mutation")
			require.Equal(t, uint64(1), client.rateLimiter.GetMetrics().Requests, "probe uses the configured Hardcover rate limiter")
		})
	}
}

func TestProbeUpsertBookCapabilityDryRunSkipsMutation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
	}))
	defer server.Close()

	client := CreateTestClient(server)
	client.dryRun = true

	got := client.ProbeUpsertBookCapability(context.Background())

	require.Equal(t, EditionCapabilityProbeAllowed, got)
	require.Zero(t, requests.Load())
}
