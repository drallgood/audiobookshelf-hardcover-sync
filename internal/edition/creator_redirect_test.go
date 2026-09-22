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

// chainTransport serves a scripted redirect chain: each URL in redirects
// answers 302 to its mapped Location, any other URL answers 200. It records
// the Authorization header seen for every URL it is asked for.
type chainTransport struct {
	redirects map[string]string
	auth      map[string]string
}

func (c *chainTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if c.auth == nil {
		c.auth = map[string]string{}
	}
	c.auth[req.URL.String()] = req.Header.Get("Authorization")
	if location, ok := c.redirects[req.URL.String()]; ok {
		return &http.Response{
			StatusCode: http.StatusFound,
			Status:     "302 Found",
			Header:     http.Header{"Location": []string{location}},
			Body:       http.NoBody,
			Request:    req,
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{},
		Body:       http.NoBody,
		Request:    req,
	}, nil
}

func TestCreatorRedirectAuthorizationAcrossSchemeChains(t *testing.T) {
	const (
		a = "https://abs.home/a"
		b = "http://abs.home/b"
	)
	tests := []struct {
		name      string
		start     string
		redirects map[string]string
		// wantAuth is the expected Authorization header per followed URL.
		wantAuth map[string]string
	}{
		{
			name:  "https to http to http stays clean after the downgrade",
			start: a,
			redirects: map[string]string{
				a: b,
				b: "http://abs.home/c",
			},
			wantAuth: map[string]string{
				a:                   "Bearer abs-secret",
				b:                   "",
				"http://abs.home/c": "",
			},
		},
		{
			name:  "https to http to https carries the token only on https hops",
			start: a,
			redirects: map[string]string{
				a: b,
				b: "https://abs.home/c",
			},
			wantAuth: map[string]string{
				a:                    "Bearer abs-secret",
				b:                    "",
				"https://abs.home/c": "Bearer abs-secret",
			},
		},
		{
			name:  "plain http origin keeps forwarding to the same host",
			start: "http://abs.home/a",
			redirects: map[string]string{
				"http://abs.home/a": b,
			},
			wantAuth: map[string]string{
				"http://abs.home/a": "Bearer abs-secret",
				b:                   "Bearer abs-secret",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := &chainTransport{redirects: tt.redirects}
			creator := NewCreator(nil, logger.Get(), false, "")
			client := *creator.httpClient // keep the production CheckRedirect, swap only the transport
			client.Transport = rt

			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, tt.start, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Authorization", "Bearer abs-secret")

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			resp.Body.Close()

			if len(rt.auth) != len(tt.wantAuth) {
				t.Fatalf("followed %d URLs %v, want %d", len(rt.auth), rt.auth, len(tt.wantAuth))
			}
			for u, want := range tt.wantAuth {
				got, ok := rt.auth[u]
				if !ok {
					t.Errorf("%s was never requested", u)
					continue
				}
				if got != want {
					t.Errorf("Authorization at %s = %q, want %q", u, got, want)
				}
			}
		})
	}
}

func TestCreatorRedirectAuthorizationStaysWithinConfiguredBase(t *testing.T) {
	const (
		origin  = "https://abs.home:1337/abs/api/items/li_1/cover"
		baseURL = "https://abs.home:1337/abs"
		token   = "Bearer abs-secret"
	)
	tests := []struct {
		name       string
		redirectTo string
		wantAuth   string
	}{
		{
			name:       "in-scope path keeps token",
			redirectTo: "https://abs.home:1337/abs/api/items/li_1/cover-large",
			wantAuth:   token,
		},
		{
			name:       "outside path strips token",
			redirectTo: "https://abs.home:1337/other/cover.jpg",
		},
		{
			name:       "different port strips token",
			redirectTo: "https://abs.home:1338/abs/api/items/li_1/cover",
		},
		{
			name:       "subdomain strips token",
			redirectTo: "https://cdn.abs.home:1337/abs/api/items/li_1/cover",
		},
		{
			name:       "https downgrade strips token",
			redirectTo: "http://abs.home:1337/abs/api/items/li_1/cover",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := &chainTransport{redirects: map[string]string{origin: tt.redirectTo}}
			creator := NewCreator(nil, logger.Get(), false, "abs-secret")
			creator.SetAudiobookshelfBaseURL(baseURL)
			client := *creator.httpClient // keep production CheckRedirect, swap only the transport
			client.Transport = rt

			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, origin, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Authorization", token)

			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			resp.Body.Close()

			if got := rt.auth[tt.redirectTo]; got != tt.wantAuth {
				t.Errorf("Authorization at %s = %q, want %q", tt.redirectTo, got, tt.wantAuth)
			}
		})
	}
}
