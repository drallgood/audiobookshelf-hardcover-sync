package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	httpclient "github.com/drallgood/audiobookshelf-hardcover-sync/http"
	"github.com/drallgood/audiobookshelf-hardcover-sync/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient(t *testing.T) {
	// Create a test config
	cfg := &httpclient.Config{
		HTTP: httpclient.HTTPConfig{
			Timeout:            30 * time.Second,
			MaxIdleConns:       100,
			IdleConnTimeout:    90 * time.Second,
			DisableCompression: false,
			RetryWaitMin:       1 * time.Second,
			RetryWaitMax:       30 * time.Second,
			MaxRetries:         3,
		},
	}

	client := httpclient.NewClient(cfg)
	assert.NotNil(t, client)
	assert.NotNil(t, client.Client)
}

func TestClient_Do_Success(t *testing.T) {
	// Setup test server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success"}`))
	}))
	defer ts.Close()

	// Create client with test config
	client := httpclient.NewClient(&httpclient.Config{
		HTTP: httpclient.HTTPConfig{
			Timeout:          5 * time.Second,
			RetryWaitMin:     10 * time.Millisecond,
			RetryWaitMax:     20 * time.Millisecond,
			MaxRetries:       1,
			MaxIdleConns:     10,
			IdleConnTimeout:  30 * time.Second,
			DisableCompression: false,
		},
	})


	// Create request
	req, err := client.NewRequest("GET", ts.URL, nil)
	require.NoError(t, err)

	// Execute request
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify response
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestClient_Do_Retry(t *testing.T) {
	// Setup test server that fails first request, then succeeds
	attempt := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempt == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			attempt++
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"success"}`))
	}))
	defer ts.Close()

	// Create client with test config
	client := httpclient.NewClient(&httpclient.Config{
		HTTP: httpclient.HTTPConfig{
			Timeout:          5 * time.Second,
			RetryWaitMin:     10 * time.Millisecond,
			RetryWaitMax:     20 * time.Millisecond,
			MaxRetries:       3,
			MaxIdleConns:     10,
			IdleConnTimeout:  30 * time.Second,
			DisableCompression: false,
		},
	})


	// Create request
	req, err := client.NewRequest("GET", ts.URL, nil)
	require.NoError(t, err)

	// Execute request
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify response after retry
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestClient_HTTP_Methods(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		handler  http.HandlerFunc
		body     interface{}
		wantCode int
	}{
		{
			name:   "GET request",
			method: "GET",
			handler: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "GET", r.Method)
				w.WriteHeader(http.StatusOK)
			},
			wantCode: http.StatusOK,
		},
		{
			name:   "POST request with body",
			method: "POST",
			handler: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "POST", r.Method)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				w.WriteHeader(http.StatusCreated)
			},
			body:     map[string]string{"key": "value"},
			wantCode: http.StatusCreated,
		},
		{
			name:   "PUT request with body",
			method: "PUT",
			handler: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "PUT", r.Method)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				w.WriteHeader(http.StatusOK)
			},
			body:     map[string]string{"key": "value"},
			wantCode: http.StatusOK,
		},
		{
			name:   "DELETE request",
			method: "DELETE",
			handler: func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "DELETE", r.Method)
				w.WriteHeader(http.StatusNoContent)
			},
			wantCode: http.StatusNoContent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test server
			ts := httptest.NewServer(tt.handler)
			defer ts.Close()

			// Create client with test config
			client := httpclient.NewClient(&httpclient.Config{
				HTTP: httpclient.HTTPConfig{
					Timeout:          5 * time.Second,
					RetryWaitMin:     10 * time.Millisecond,
					RetryWaitMax:     20 * time.Millisecond,
					MaxRetries:       3,
					MaxIdleConns:     10,
					IdleConnTimeout:  30 * time.Second,
					DisableCompression: false,
				},
			})


			// Create and execute request based on method
			var resp *http.Response
			var err error

			switch tt.method {
			case "GET":
				resp, err = client.Get(ts.URL)
			case "POST":
				resp, err = client.Post(ts.URL, tt.body)
			case "PUT":
				resp, err = client.Put(ts.URL, tt.body)
			case "DELETE":
				resp, err = client.Delete(ts.URL)
			}

			require.NoError(t, err)
			defer resp.Body.Close()

			// Verify response
			assert.Equal(t, tt.wantCode, resp.StatusCode)
		})
	}
}

func init() {
	// Initialize logger for tests
	logging.InitForTesting()
}
