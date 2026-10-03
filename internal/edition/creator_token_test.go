package edition

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

// A whitespace-only base URL must not accidentally scope the token to a host.
func TestCreatorWhitespaceBaseURLWithholdsToken(t *testing.T) {
	const token = "abs-secret"

	tests := []struct {
		name     string
		baseURL  string
		imageURL string
	}{
		{"whitespace base withholds token", "  \t", "https://audiobookshelf.example.com/api/items/li_1/cover"},
		{"whitespace base, non-matching host withheld", "  \t", "https://abs.home/api/items/li_1/cover"},
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
			if rt.auth != "" {
				t.Errorf("unexpected Authorization header %q", rt.auth)
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
		{name: "localhost with port is normalized to https", baseURL: "localhost:13378", wantStored: "https://localhost:13378"},
		{name: "opaque ftp scheme is rejected", baseURL: "ftp:443", wantErr: true},
		{name: "ambiguous short host with port requires scheme", baseURL: "abs:13378", wantErr: true},
		{name: "explicit short host with port is allowed", baseURL: "http://abs:13378", wantStored: "http://abs:13378"},
		{name: "bare host with path is rejected", baseURL: "abs.home/api", wantErr: true},
		{name: "bare host with userinfo is rejected", baseURL: "user@abs.home", wantErr: true},
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

func TestCreatorCoverFetchUsesAudiobookshelfNetworkTrustPolicy(t *testing.T) {
	creator := NewCreator(nil, logger.Get(), false, "abs-secret")
	if err := creator.SetAudiobookshelfNetworkTrust("public_only"); err != nil {
		t.Fatalf("SetAudiobookshelfNetworkTrust() error = %v", err)
	}
	if err := creator.SetAudiobookshelfBaseURL("http://127.0.0.1:13378"); err == nil {
		t.Fatal("SetAudiobookshelfBaseURL() accepted HTTP with public_only trust")
	}
	if err := creator.SetAudiobookshelfBaseURL("https://127.0.0.1:13378"); err != nil {
		t.Fatalf("SetAudiobookshelfBaseURL() error = %v", err)
	}

	_, err := creator.uploadImageToGCS(context.Background(), 1, "https://127.0.0.1:13378/api/items/item/cover")
	if err == nil || !strings.Contains(err.Error(), "not allowed by public_only") {
		t.Fatalf("uploadImageToGCS() error = %v, want policy rejection before network access", err)
	}

	_, err = creator.uploadImageToGCS(context.Background(), 1, "https://169.254.169.254/cover.jpg")
	if err == nil || !strings.Contains(err.Error(), "not allowed by public_only") {
		t.Fatalf("off-origin cover error = %v, want policy rejection", err)
	}
}

func TestCreatorCoverFetchChecksOffOriginRedirect(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Redirect(w, r, "http://169.254.169.254/cover.jpg", http.StatusFound)
	}))
	defer server.Close()

	creator := NewCreator(nil, logger.Get(), false, "abs-secret")
	if err := creator.SetAudiobookshelfBaseURL("http://abs.example"); err != nil {
		t.Fatalf("SetAudiobookshelfBaseURL() error = %v", err)
	}
	_, err := creator.uploadImageToGCS(context.Background(), 1, server.URL+"/cover.jpg")
	if err == nil || !strings.Contains(err.Error(), "redirect destination rejected") {
		t.Fatalf("off-origin redirect error = %v, want policy rejection", err)
	}
	if requests != 1 {
		t.Fatalf("cover server requests = %d, want 1", requests)
	}
}

func TestNewCreatorTLSVerificationRequiresExplicitOptIn(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("not a valid JPEG"))
	}))
	defer server.Close()

	creator := NewCreator(nil, logger.Get(), false, "")
	_, err := creator.uploadImageToGCS(context.Background(), 1, server.URL)
	var verificationErr *tls.CertificateVerificationError
	if !errors.As(err, &verificationErr) {
		t.Fatalf("uploadImageToGCS() error = %v, want certificate verification failure", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("TLS handler requests before opt-in = %d, want 0", got)
	}

	creator.EnableInsecureTLS()
	_, err = creator.uploadImageToGCS(context.Background(), 1, server.URL)
	if !errors.Is(err, errCoverFormat) {
		t.Fatalf("uploadImageToGCS() after opt-in error = %v, want response body format validation", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("TLS handler requests after opt-in = %d, want 1", got)
	}
}
