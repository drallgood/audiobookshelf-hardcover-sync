package hardcover

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProbeInsertEditionCapabilityClassifiesMutationResponses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   EditionCapabilityProbeState
	}{
		{
			name:   "observed impossible book response proves scope",
			status: http.StatusOK,
			body:   `{"errors":[{"message":"Couldn't find Book"}],"data":null}`,
			want:   EditionCapabilityProbeAllowed,
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
				require.Contains(t, payload.Query, "insert_edition")
				require.Contains(t, payload.Query, "book_id: -1")
				require.Contains(t, payload.Query, "reading_format_id: 4")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			client := CreateTestClient(server)
			got := client.ProbeInsertEditionCapability(context.Background())

			require.Equal(t, test.want, got)
			require.Equal(t, int32(1), requests.Load(), "probe sends one actual mutation with an impossible book ID")
			require.Equal(t, uint64(1), client.rateLimiter.GetMetrics().Requests, "probe uses the configured Hardcover rate limiter")
		})
	}
}

func TestProbeInsertEditionCapabilityTimeoutIsUnverified(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		time.Sleep(250 * time.Millisecond)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := CreateTestClient(server)
	client.httpClient.Timeout = 100 * time.Millisecond

	got := client.ProbeInsertEditionCapability(context.Background())

	require.Equal(t, EditionCapabilityProbeUnverified, got)
	require.Equal(t, int32(1), requests.Load())
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

func TestProbeInsertEditionCapabilityDoesNotAcceptNearMatchNotFoundText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"Couldn't find Book for another reason"}],"data":null}`))
	}))
	defer server.Close()

	got := CreateTestClient(server).ProbeInsertEditionCapability(context.Background())

	require.Equal(t, EditionCapabilityProbeUnverified, got)
}
