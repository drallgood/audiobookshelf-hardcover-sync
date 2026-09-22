package edition

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

// recordingTransport captures the first request and then fails it so nothing
// reaches the network (or hardcover.app) during the test.
type recordingTransport struct {
	auth    string
	called  bool
	failure error
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !r.called {
		r.called = true
		r.auth = req.Header.Get("Authorization")
	}
	return nil, r.failure
}

func TestCreatorAudiobookshelfTokenScoping(t *testing.T) {
	const token = "abs-secret"

	tests := []struct {
		name      string
		baseURL   string
		imageURL  string
		wantToken bool
	}{
		{"configured base sends token for any host name", "https://abs.home", "https://abs.home/api/items/li_1/cover", true},
		{"configured base with trailing slash", "https://abs.home/", "https://abs.home/api/items/li_1/cover", true},
		{"base with path prefix", "https://example.com/abs", "https://example.com/abs/api/items/li_1/cover", true},
		{"host comparison ignores case", "https://ABS.home", "https://abs.HOME/api/items/li_1/cover", true},
		{"other host withheld", "https://abs.home", "https://cdn.example.com/cover.jpg", false},
		{"lookalike host withheld", "https://abs.home", "https://audiobookshelf.evil.example/cover.jpg", false},
		{"lookalike host withheld even with legacy keyword", "https://abs.home", "https://abs.home.evil.example/audiobookshelf/cover.jpg", false},
		{"sibling path withheld", "https://example.com/abs", "https://example.com/absolute/cover.jpg", false},
		{"path outside base withheld", "https://example.com/abs", "https://example.com/other/cover.jpg", false},
		{"scheme mismatch withheld", "https://abs.home", "http://abs.home/api/items/li_1/cover", false},
		{"port mismatch withheld", "https://abs.home:13378", "https://abs.home/api/items/li_1/cover", false},
		{"unset base withholds token", "", "https://audiobookshelf.example.com/api/items/li_1/cover", false},
		{"unset base withholds token for any host", "", "https://abs.home/api/items/li_1/cover", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := &recordingTransport{failure: errors.New("stop after recording")}
			creator := NewCreatorWithHTTPClient(nil, logger.Get(), false, token, &http.Client{Transport: rt})
			if tt.baseURL != "" {
				if err := creator.SetAudiobookshelfBaseURL(tt.baseURL); err != nil {
					t.Fatalf("SetAudiobookshelfBaseURL() error = %v", err)
				}
			}

			if _, err := creator.uploadImageToGCS(context.Background(), 1, tt.imageURL); err == nil {
				t.Fatal("expected the recorded download to fail")
			}
			if !rt.called {
				t.Fatal("image download was never attempted")
			}

			got := rt.auth == "Bearer "+token
			if got != tt.wantToken {
				t.Errorf("token sent = %v, want %v (Authorization=%q)", got, tt.wantToken, rt.auth)
			}
			if !tt.wantToken && rt.auth != "" {
				t.Errorf("unexpected Authorization header %q", rt.auth)
			}
		})
	}
}

// An empty or whitespace-only base URL, as the commands pass when no
// Audiobookshelf URL is configured, must withhold the token.
func TestCreatorEmptyBaseURLWithholdsToken(t *testing.T) {
	const token = "abs-secret"

	tests := []struct {
		name      string
		baseURL   string
		imageURL  string
		wantToken bool
	}{
		{"empty base withholds token", "", "https://audiobookshelf.example.com/api/items/li_1/cover", false},
		{"empty base, non-matching host withheld", "", "https://abs.home/api/items/li_1/cover", false},
		{"whitespace base withholds token", "  \t", "https://audiobookshelf.example.com/api/items/li_1/cover", false},
		{"whitespace base, non-matching host withheld", "  \t", "https://abs.home/api/items/li_1/cover", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := &recordingTransport{failure: errors.New("stop after recording")}
			creator := NewCreatorWithHTTPClient(nil, logger.Get(), false, token, &http.Client{Transport: rt})
			if err := creator.SetAudiobookshelfBaseURL(tt.baseURL); err != nil {
				t.Fatalf("SetAudiobookshelfBaseURL() error = %v", err)
			}

			if _, err := creator.uploadImageToGCS(context.Background(), 1, tt.imageURL); err == nil {
				t.Fatal("expected the recorded download to fail")
			}
			if !rt.called {
				t.Fatal("image download was never attempted")
			}
			if got := rt.auth == "Bearer "+token; got != tt.wantToken {
				t.Errorf("token sent = %v, want %v (Authorization=%q)", got, tt.wantToken, rt.auth)
			}
		})
	}
}

func TestSetAudiobookshelfBaseURLValidation(t *testing.T) {
	tests := []struct {
		name       string
		baseURL    string
		wantErr    bool
		wantStored string
	}{
		{name: "empty is unset", baseURL: "", wantStored: ""},
		{name: "whitespace is unset", baseURL: " \t", wantStored: ""},
		{name: "http is allowed", baseURL: "http://abs.home:13378", wantStored: "http://abs.home:13378"},
		{name: "https path is allowed", baseURL: "https://abs.home/api", wantStored: "https://abs.home/api"},
		// A bare host:port (no scheme) is the footgun a maintainer review
		// flagged: it must not be silently misread or rejected outright, so
		// it is normalized to https rather than failing the command.
		{name: "bare host:port is normalized to https", baseURL: "abs.home:13378", wantStored: "https://abs.home:13378"},
		{name: "bare hostname is normalized to https", baseURL: "abs.home", wantStored: "https://abs.home"},
		{name: "host is required", baseURL: "https:///api", wantErr: true},
		{name: "hostname is required", baseURL: "http://:13378", wantErr: true},
		{name: "http or https is required", baseURL: "ftp://abs.home", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creator := &Creator{}
			err := creator.SetAudiobookshelfBaseURL(tt.baseURL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("SetAudiobookshelfBaseURL() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && creator.audiobookshelfBaseURL != tt.wantStored {
				t.Errorf("stored base URL = %q, want %q", creator.audiobookshelfBaseURL, tt.wantStored)
			}
		})
	}
}

// TestNewCreatorTLSVerificationRequiresExplicitOptIn is in package edition
// (not edition_test) so it can read creator.httpClient directly, without the
// reflect/unsafe access other tests in this package's external test file use.
func TestNewCreatorTLSVerificationRequiresExplicitOptIn(t *testing.T) {
	creator := NewCreator(nil, logger.Get(), false, "")

	transport, ok := creator.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("httpClient.Transport = %T, want *http.Transport", creator.httpClient.Transport)
	}
	if transport.TLSClientConfig != nil {
		t.Errorf("TLSClientConfig = %+v, want nil (Go's verified TLS settings) before EnableInsecureTLS", transport.TLSClientConfig)
	}

	creator.EnableInsecureTLS()

	transport, ok = creator.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("httpClient.Transport = %T, want *http.Transport", creator.httpClient.Transport)
	}
	if transport.TLSClientConfig == nil || !transport.TLSClientConfig.InsecureSkipVerify {
		t.Errorf("TLSClientConfig = %+v, want InsecureSkipVerify=true after EnableInsecureTLS", transport.TLSClientConfig)
	}
}
