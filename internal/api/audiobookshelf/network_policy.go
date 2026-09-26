package audiobookshelf

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	// NetworkTrustAllowPrivate permits globally routable addresses and the
	// private, shared, unique-local, and loopback ranges commonly used by
	// self-hosted Audiobookshelf deployments.
	NetworkTrustAllowPrivate = "allow_private"
	// NetworkTrustPublicOnly permits only globally routable unicast addresses
	// over HTTPS.
	NetworkTrustPublicOnly = "public_only"
)

type ipResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

type networkPolicy struct {
	trust    string
	resolver ipResolver
}

// Transports hold only connection state. Request credentials and base paths
// remain in each client's scopedRoundTripper.
var networkTransports = map[string]*http.Transport{
	NetworkTrustAllowPrivate: newNetworkTransport(NetworkTrustAllowPrivate),
	NetworkTrustPublicOnly:   newNetworkTransport(NetworkTrustPublicOnly),
}

func newNetworkTransport(trust string) *http.Transport {
	policy := networkPolicy{trust: trust, resolver: net.DefaultResolver}
	return &http.Transport{
		DialContext:            policy.dialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           100,
		MaxIdleConnsPerHost:    2,
		IdleConnTimeout:        90 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: 10 << 20,
	}
}

type scopedRoundTripper struct {
	base   *url.URL
	token  string
	policy networkPolicy
	next   http.RoundTripper
}

func (rt scopedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := rt.policy.validateURLSyntax(req.URL); err != nil {
		return nil, fmt.Errorf("Audiobookshelf destination rejected: %w", err)
	}
	request := req.Clone(req.Context())
	request.URL.Scheme = strings.ToLower(request.URL.Scheme)
	if requestWithinBase(rt.base, request.URL) {
		request.Header.Set("Authorization", "Bearer "+rt.token)
	} else {
		request.Header.Del("Authorization")
	}
	return rt.next.RoundTrip(request)
}

// ValidateBaseURL checks and normalizes the syntax of an Audiobookshelf base
// URL. DNS results are intentionally checked at connection time.
func ValidateBaseURL(baseURL, networkTrust string) (string, error) {
	if err := validateNetworkTrust(networkTrust); err != nil {
		return "", err
	}
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid Audiobookshelf URL: %w", err)
	}
	if !u.IsAbs() || u.Opaque != "" || u.Host == "" || u.Hostname() == "" {
		return "", errors.New("Audiobookshelf URL must be an absolute URL with a host")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("Audiobookshelf URL scheme must be http or https")
	}
	if networkTrust == NetworkTrustPublicOnly && u.Scheme != "https" {
		return "", errors.New("Audiobookshelf URL must use https with public_only network trust")
	}
	if u.User != nil {
		return "", errors.New("Audiobookshelf URL must not contain user information")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return "", errors.New("Audiobookshelf URL must not contain a query")
	}
	if strings.Contains(baseURL, "#") || u.Fragment != "" {
		return "", errors.New("Audiobookshelf URL must not contain a fragment")
	}
	if strings.Contains(u.Hostname(), "%") {
		return "", errors.New("Audiobookshelf URL must not contain an IPv6 zone identifier")
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", errors.New("Audiobookshelf URL has an empty port")
	}
	if port := u.Port(); port != "" {
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return "", errors.New("Audiobookshelf URL has an invalid port")
		}
	}
	if u.Path == "" {
		u.Path = "/"
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path == "" {
		u.Path = "/"
	}
	u.RawPath = ""
	return strings.TrimRight(u.String(), "/"), nil
}

// NewHTTPClientWithNetworkTrust returns an HTTP client for requests to the
// configured Audiobookshelf server. Use it only for ABS requests; it applies
// the same destination checks and bearer-token redirect scoping as Client.
func NewHTTPClientWithNetworkTrust(baseURL, token, networkTrust string) (*http.Client, error) {
	normalizedURL, err := ValidateBaseURL(baseURL, networkTrust)
	if err != nil {
		return nil, err
	}
	return newHTTPClient(normalizedURL, token, networkTrust)
}

func newHTTPClient(baseURL, token, networkTrust string) (*http.Client, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Audiobookshelf URL: %w", err)
	}
	policy := networkPolicy{trust: networkTrust, resolver: net.DefaultResolver}
	// Proxies resolve the destination on our behalf and would bypass the
	// address checks in the dialer. Share the connection pool across clients
	// with the same trust mode so short-lived draft clients do not leave a pool
	// of idle connections behind after each request.
	transport := networkTransports[networkTrust]
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: scopedRoundTripper{base: base, token: token, policy: policy, next: transport},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if err := policy.validateURL(req.Context(), req.URL); err != nil {
				return fmt.Errorf("redirect destination rejected: %w", err)
			}
			req.URL.Scheme = strings.ToLower(req.URL.Scheme)
			if requestWithinBase(base, req.URL) {
				req.Header.Set("Authorization", "Bearer "+token)
			} else {
				req.Header.Del("Authorization")
			}
			return nil
		},
	}, nil
}

func validateNetworkTrust(networkTrust string) error {
	switch networkTrust {
	case NetworkTrustAllowPrivate, NetworkTrustPublicOnly:
		return nil
	default:
		return fmt.Errorf("unsupported Audiobookshelf network trust %q (expected %q or %q)", networkTrust, NetworkTrustAllowPrivate, NetworkTrustPublicOnly)
	}
}

func (p networkPolicy) validateURL(ctx context.Context, u *url.URL) error {
	if err := p.validateURLSyntax(u); err != nil {
		return err
	}
	_, err := p.resolveAllowed(ctx, u.Hostname())
	return err
}

func (p networkPolicy) validateURLSyntax(u *url.URL) error {
	if u == nil || u.Hostname() == "" {
		return errors.New("destination URL must have a host")
	}
	if u.User != nil {
		return errors.New("destination URL must not contain user information")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("destination scheme %q is not allowed", u.Scheme)
	}
	if p.trust == NetworkTrustPublicOnly && scheme != "https" {
		return errors.New("public_only network trust requires https")
	}
	if strings.Contains(u.Hostname(), "%") {
		return errors.New("destination URL must not contain an IPv6 zone identifier")
	}
	if strings.HasSuffix(u.Host, ":") {
		return errors.New("destination URL has an empty port")
	}
	if port := u.Port(); port != "" {
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return errors.New("destination URL has an invalid port")
		}
	}
	return nil
}

func (p networkPolicy) resolveAllowed(ctx context.Context, host string) ([]net.IPAddr, error) {
	if ip := net.ParseIP(host); ip != nil {
		if !addressAllowed(ip, p.trust) {
			return nil, fmt.Errorf("destination address %s is not allowed by %s", ip, p.trust)
		}
		return []net.IPAddr{{IP: ip}}, nil
	}

	addresses, err := p.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve destination %q: %w", host, err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("destination %q resolved to no addresses", host)
	}
	for _, address := range addresses {
		if !addressAllowed(address.IP, p.trust) {
			return nil, fmt.Errorf("destination %q resolved to disallowed address %s", host, address.IP)
		}
	}
	return addresses, nil
}

func (p networkPolicy) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid destination address %q: %w", address, err)
	}
	addresses, err := p.resolveAllowed(ctx, host)
	if err != nil {
		return nil, err
	}

	dialer := net.Dialer{}
	var dialErrors []error
	for _, address := range addresses {
		ip, ok := netip.AddrFromSlice(address.IP)
		if !ok {
			dialErrors = append(dialErrors, fmt.Errorf("invalid resolved IP address %q", address.IP))
			continue
		}
		ip = ip.Unmap()
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err != nil {
			dialErrors = append(dialErrors, err)
			continue
		}
		peerIP, err := peerIP(conn.RemoteAddr())
		if err != nil || !addressAllowed(peerIP, p.trust) {
			_ = conn.Close()
			if err != nil {
				dialErrors = append(dialErrors, fmt.Errorf("cannot verify connected peer: %w", err))
			} else {
				dialErrors = append(dialErrors, fmt.Errorf("connected peer address %s is not allowed by %s", peerIP, p.trust))
			}
			continue
		}
		return conn, nil
	}
	if len(dialErrors) == 0 {
		return nil, fmt.Errorf("destination %q has no usable addresses", host)
	}
	return nil, fmt.Errorf("could not connect to validated destination %q: %w", host, errors.Join(dialErrors...))
}

func peerIP(addr net.Addr) (net.IP, error) {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return nil, err
	}
	parsed, err := netip.ParseAddr(host)
	if err != nil {
		return nil, fmt.Errorf("peer %q is not an IP address", host)
	}
	return net.ParseIP(parsed.WithZone("").String()), nil
}

func addressAllowed(ip net.IP, networkTrust string) bool {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsUnspecified() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return false
	}

	if addr.Is4() {
		if inPrefixes(addr, "0.0.0.0/8", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4") {
			return false
		}
		if addr.IsLoopback() || addr.IsPrivate() || inPrefixes(addr, "100.64.0.0/10") {
			return networkTrust == NetworkTrustAllowPrivate
		}
		return addr.IsGlobalUnicast()
	}

	// Only the global-unicast allocation (2000::/3) is public. Explicitly
	// exclude protocol, transition, documentation, and benchmarking ranges.
	if inPrefixes(addr, "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20") {
		return false
	}
	if addr.IsLoopback() || addr.IsPrivate() {
		return networkTrust == NetworkTrustAllowPrivate
	}
	return addr.IsGlobalUnicast() && inPrefixes(addr, "2000::/3")
}

func inPrefixes(addr netip.Addr, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if netip.MustParsePrefix(prefix).Contains(addr) {
			return true
		}
	}
	return false
}

func requestWithinBase(base, target *url.URL) bool {
	if base == nil || target == nil || target.User != nil || !sameOrigin(base, target) {
		return false
	}
	basePath := normalizedURLPath(base)
	targetPath := normalizedURLPath(target)
	return basePath == "/" || targetPath == basePath || strings.HasPrefix(targetPath, basePath+"/")
}

func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) && effectivePort(a) == effectivePort(b)
}

func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}

func normalizedURLPath(u *url.URL) string {
	if u.Path == "" {
		return "/"
	}
	clean := path.Clean("/" + strings.TrimPrefix(u.Path, "/"))
	if clean == "." {
		return "/"
	}
	return clean
}
