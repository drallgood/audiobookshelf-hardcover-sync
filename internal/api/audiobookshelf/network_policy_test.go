package audiobookshelf

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateBaseURL(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		trust       string
		wantURL     string
		wantErrText string
	}{
		{
			name:    "normalizes a trailing slash",
			baseURL: "http://audiobookshelf.local:13378/abs/",
			trust:   NetworkTrustAllowPrivate,
			wantURL: "http://audiobookshelf.local:13378/abs",
		},
		{
			name:        "requires an absolute URL",
			baseURL:     "/audiobookshelf",
			trust:       NetworkTrustAllowPrivate,
			wantErrText: "absolute URL",
		},
		{
			name:        "rejects unsupported schemes",
			baseURL:     "ftp://audiobookshelf.local",
			trust:       NetworkTrustAllowPrivate,
			wantErrText: "scheme must be http or https",
		},
		{
			name:        "rejects public-only HTTP",
			baseURL:     "http://audiobookshelf.example",
			trust:       NetworkTrustPublicOnly,
			wantErrText: "must use https",
		},
		{
			name:        "rejects credentials",
			baseURL:     "https://user:password@audiobookshelf.example",
			trust:       NetworkTrustPublicOnly,
			wantErrText: "user information",
		},
		{
			name:        "rejects query strings",
			baseURL:     "https://audiobookshelf.example?target=internal",
			trust:       NetworkTrustPublicOnly,
			wantErrText: "query",
		},
		{
			name:        "rejects fragments",
			baseURL:     "https://audiobookshelf.example/#section",
			trust:       NetworkTrustPublicOnly,
			wantErrText: "fragment",
		},
		{
			name:        "rejects malformed ports",
			baseURL:     "https://audiobookshelf.example:abc",
			trust:       NetworkTrustPublicOnly,
			wantErrText: "invalid Audiobookshelf URL",
		},
		{
			name:        "rejects invalid trust values",
			baseURL:     "https://audiobookshelf.example",
			trust:       "private",
			wantErrText: "unsupported Audiobookshelf network trust",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateBaseURL(tt.baseURL, tt.trust)
			if tt.wantErrText != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrText)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantURL, got)
		})
	}
}

func TestClientsReuseConnectionsWithoutSharingCredentials(t *testing.T) {
	requests := make(chan struct{ peer, token string }, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- struct{ peer, token string }{r.RemoteAddr, r.Header.Get("Authorization")}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	for _, token := range []string{"first", "second"} {
		client, err := NewHTTPClientWithNetworkTrust(server.URL, token, NetworkTrustAllowPrivate)
		require.NoError(t, err)
		response, err := client.Get(server.URL)
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
	}

	first, second := <-requests, <-requests
	assert.Equal(t, first.peer, second.peer, "short-lived clients should reuse the connection pool")
	assert.Equal(t, "Bearer first", first.token)
	assert.Equal(t, "Bearer second", second.token)
}

func TestAddressAllowed(t *testing.T) {
	tests := []struct {
		address string
		trust   string
		allowed bool
	}{
		{"8.8.8.8", NetworkTrustAllowPrivate, true},
		{"8.8.8.8", NetworkTrustPublicOnly, true},
		{"192.168.1.4", NetworkTrustAllowPrivate, true},
		{"192.168.1.4", NetworkTrustPublicOnly, false},
		{"100.100.1.1", NetworkTrustAllowPrivate, true},
		{"100.100.1.1", NetworkTrustPublicOnly, false},
		{"127.0.0.1", NetworkTrustAllowPrivate, true},
		{"127.0.0.1", NetworkTrustPublicOnly, false},
		{"fd00::1", NetworkTrustAllowPrivate, true},
		{"fd00::1", NetworkTrustPublicOnly, false},
		{"169.254.169.254", NetworkTrustAllowPrivate, false},
		{"fe80::1", NetworkTrustAllowPrivate, false},
		{"192.0.2.10", NetworkTrustAllowPrivate, false},
		{"198.18.0.1", NetworkTrustAllowPrivate, false},
		{"2001:db8::1", NetworkTrustAllowPrivate, false},
		{"::ffff:127.0.0.1", NetworkTrustAllowPrivate, true},
		{"::ffff:127.0.0.1", NetworkTrustPublicOnly, false},
	}
	for _, tt := range tests {
		t.Run(tt.address+"/"+tt.trust, func(t *testing.T) {
			assert.Equal(t, tt.allowed, addressAllowed(net.ParseIP(tt.address), tt.trust))
		})
	}
}

type fixedResolver []net.IPAddr

func (r fixedResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return []net.IPAddr(r), nil
}

type intermittentResolver struct{ unavailable atomic.Bool }

func (r *intermittentResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	if r.unavailable.Load() {
		return nil, errors.New("DNS unavailable")
	}
	return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
}

func TestExistingAllowedConnectionSurvivesDNSFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	require.NoError(t, err)
	base, err := url.Parse("http://abs.example:" + port)
	require.NoError(t, err)

	resolver := &intermittentResolver{}
	policy := networkPolicy{trust: NetworkTrustAllowPrivate, resolver: resolver}
	client := &http.Client{Transport: scopedRoundTripper{
		base: base, token: "secret", policy: policy,
		next: &http.Transport{DialContext: policy.dialContext},
	}}
	for attempt := range 2 {
		response, err := client.Get(base.String())
		require.NoError(t, err, "request %d should use the validated connection", attempt+1)
		_, err = io.Copy(io.Discard, response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		resolver.unavailable.Store(true)
	}
}

func TestResolveAllowedRejectsMixedDNSAnswers(t *testing.T) {
	policy := networkPolicy{
		trust:    NetworkTrustAllowPrivate,
		resolver: fixedResolver{{IP: net.ParseIP("127.0.0.1")}, {IP: net.ParseIP("169.254.169.254")}},
	}
	_, err := policy.resolveAllowed(context.Background(), "abs.example")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "disallowed address 169.254.169.254")
}

func TestNewClientWithNetworkTrustRejectsLoopbackInPublicOnly(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"libraries":[]}`))
	}))
	defer server.Close()

	client, err := NewClientWithNetworkTrust(server.URL, "secret", NetworkTrustPublicOnly)
	require.NoError(t, err, "the URL is syntactically valid HTTPS")
	_, err = client.GetLibraries(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed by public_only")
	assert.Zero(t, requests, "blocked destinations must not receive the request")
}

func TestRedirectRejectsMetadataAddress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "http://169.254.169.254/latest/meta-data")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	client, err := NewClientWithNetworkTrust(server.URL, "secret", NetworkTrustAllowPrivate)
	require.NoError(t, err)
	_, err = client.GetLibraries(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redirect destination rejected")
	assert.Contains(t, err.Error(), "169.254.169.254")
}

func TestRedirectStripsBearerTokenOutsideConfiguredBasePath(t *testing.T) {
	redirectedAuthorization := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/abs/api/libraries" {
			http.Redirect(w, r, "/outside", http.StatusFound)
			return
		}
		redirectedAuthorization <- r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"libraries":[]}`))
	}))
	defer server.Close()

	client, err := NewClientWithNetworkTrust(server.URL+"/abs", "secret", NetworkTrustAllowPrivate)
	require.NoError(t, err)
	_, err = client.GetLibraries(context.Background())
	require.NoError(t, err)
	assert.Empty(t, <-redirectedAuthorization)
}

func TestRedirectStripsBearerTokenForDifferentOrigin(t *testing.T) {
	redirectedAuthorization := make(chan string, 1)
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedAuthorization <- r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"libraries":[]}`))
	}))
	defer redirectTarget.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/libraries" {
			w.Header().Set("Location", redirectTarget.URL+"/libraries")
			w.WriteHeader(http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client, err := NewClientWithNetworkTrust(server.URL, "secret", NetworkTrustAllowPrivate)
	require.NoError(t, err)
	_, err = client.GetLibraries(context.Background())
	require.NoError(t, err)
	assert.Empty(t, <-redirectedAuthorization)
}

func TestRequestWithinBaseRequiresSameOriginAndPathBoundary(t *testing.T) {
	base, err := url.Parse("https://abs.example:443/library")
	require.NoError(t, err)
	tests := []struct {
		target string
		want   bool
	}{
		{"https://abs.example/library/api/items", true},
		{"https://abs.example/library", true},
		{"https://abs.example/library-other", false},
		{"https://abs.example:444/library/api", false},
		{"http://abs.example/library/api", false},
		{"https://abs.example/other", false},
		{"https://user@abs.example/library", false},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			target, err := url.Parse(tt.target)
			require.NoError(t, err)
			assert.Equal(t, tt.want, requestWithinBase(base, target))
		})
	}
}

func TestNetworkTrustHTTPClientRejectsUnsupportedSchemeBeforeDial(t *testing.T) {
	client, err := NewHTTPClientWithNetworkTrust("https://example.com/abs", "secret", NetworkTrustPublicOnly)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodGet, "http://example.com/abs/api", nil)
	require.NoError(t, err)
	_, err = client.Do(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires https")
}
