package hardcover

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This guards the constructor's retained gqlClient wiring; Client.GraphQLQuery has separate wire-boundary coverage.
func TestNewClientWithConfigAuthenticatedGraphQLTransportComposition(t *testing.T) {
	logger.Setup(logger.Config{Level: "error", Format: "json"})

	tests := []struct {
		name            string
		status          int
		body            string
		wantBookID      int
		wantErrorStatus int
	}{
		{
			name:       "forwards authenticated request and response body",
			status:     http.StatusOK,
			body:       `{"data":{"books":[{"id":42}]}}`,
			wantBookID: 42,
		},
		{
			name:            "forwards HTTP failure status and body",
			status:          http.StatusBadGateway,
			body:            "upstream unavailable",
			wantErrorStatus: http.StatusBadGateway,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/graphql", r.URL.Path)
				assert.Equal(t, "Bearer composition-token", r.Header.Get("Authorization"))
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()

			config := DefaultClientConfig()
			config.BaseURL = server.URL + "/graphql"
			config.Timeout = time.Second
			config.RateLimit = time.Nanosecond
			client := NewClientWithConfig(config, "composition-token", logger.Get())

			var result struct {
				Books []struct {
					ID int `graphql:"id"`
				} `graphql:"books"`
			}
			err := client.gqlClient.Query(context.Background(), &result, nil)
			if test.wantErrorStatus != 0 {
				require.Error(t, err)
				var networkError interface {
					error
					StatusCode() int
					Body() string
				}
				require.ErrorAs(t, err, &networkError)
				assert.Equal(t, test.wantErrorStatus, networkError.StatusCode())
				assert.Equal(t, test.body, networkError.Body())
				return
			}

			require.NoError(t, err)
			require.Len(t, result.Books, 1)
			assert.Equal(t, test.wantBookID, result.Books[0].ID)
		})
	}
}
