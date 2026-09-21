package edition

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
)

func TestCreatorRedirectDoesNotForwardAuthorizationToOtherHost(t *testing.T) {
	var gotByTarget string
	targetHit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHit = true
		gotByTarget = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	// net/http compares host names without ports, so the redirect target must
	// use a different name (localhost) than the origin (127.0.0.1).
	targetURL, err := url.Parse(target.URL)
	if err != nil {
		t.Fatalf("parse target URL: %v", err)
	}
	redirectTo := "http://localhost:" + targetURL.Port() + "/cover.jpg"

	var gotByOrigin string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotByOrigin = r.Header.Get("Authorization")
		http.Redirect(w, r, redirectTo, http.StatusFound)
	}))
	defer origin.Close()

	creator := NewCreator(nil, logger.Get(), false, "abs-secret")
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, origin.URL+"/api/items/li_1/cover", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer abs-secret")

	resp, err := creator.httpClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if gotByOrigin != "Bearer abs-secret" {
		t.Fatalf("origin Authorization = %q, want the bearer token", gotByOrigin)
	}
	if !targetHit {
		t.Fatal("redirect to the other host was never followed")
	}
	if gotByTarget != "" {
		t.Errorf("Authorization leaked to the redirect target: %q", gotByTarget)
	}
}

func TestCreatorRedirectStopsAfterTenRedirects(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Redirect(w, r, "/again", http.StatusFound)
	}))
	defer server.Close()

	creator := NewCreator(nil, logger.Get(), false, "")
	resp, err := creator.httpClient.Get(server.URL)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "stopped after 10 redirects") {
		t.Fatalf("error = %v, want it to report stopping after 10 redirects", err)
	}
	if requests != 10 {
		t.Errorf("server saw %d requests, want 10", requests)
	}
}

// downgradeTransport answers the https request with a redirect to plain http
// on the same host and records the Authorization header of the followed request.
type downgradeTransport struct {
	followed     bool
	followedAuth string
}

func (d *downgradeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == "https" {
		return &http.Response{
			StatusCode: http.StatusFound,
			Status:     "302 Found",
			Header:     http.Header{"Location": []string{"http://abs.home/api/items/li_1/cover"}},
			Body:       http.NoBody,
			Request:    req,
		}, nil
	}
	d.followed = true
	d.followedAuth = req.Header.Get("Authorization")
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{},
		Body:       http.NoBody,
		Request:    req,
	}, nil
}

func TestCreatorRedirectDoesNotForwardAuthorizationOnHTTPSDowngrade(t *testing.T) {
	rt := &downgradeTransport{}
	creator := NewCreator(nil, logger.Get(), false, "")
	client := *creator.httpClient // keep the production CheckRedirect, swap only the transport
	client.Transport = rt

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://abs.home/api/items/li_1/cover", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer abs-secret")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()

	if !rt.followed {
		t.Fatal("downgrade redirect was never followed")
	}
	if rt.followedAuth != "" {
		t.Errorf("Authorization sent over the https -> http downgrade: %q", rt.followedAuth)
	}
}
