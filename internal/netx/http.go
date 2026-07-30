// Package netx provides safe HTTP client construction with SSRF protection and redirect controls.
package netx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const (
	defaultHTTPTimeout = 20 * time.Second
	maxRedirects       = 5
)

// ClientPolicy controls host validation behavior for client requests.
type ClientPolicy struct {
	AllowPrivateHosts bool
}

// NewHTTPClient builds an HTTP client with safe defaults and SSRF checks.
func NewHTTPClient(timeout time.Duration, policy ClientPolicy) *http.Client {
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	// E-02: Only consult environment proxy variables when private hosts are
	// allowed. When AllowPrivateHosts is false (the safe-client path), a
	// user-controlled HTTP_PROXY/HTTPS_PROXY env var could route requests
	// through an attacker-supplied proxy before the IP check fires.
	var proxyFunc func(*http.Request) (*url.URL, error)
	if policy.AllowPrivateHosts {
		proxyFunc = http.ProxyFromEnvironment
	}

	transport := &http.Transport{
		Proxy: proxyFunc,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}

			if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
				// Literal IP address — check directly.
				if !policy.AllowPrivateHosts && isBlockedAddr(ip) {
					return nil, fmt.Errorf("blocked address: %s", ip.String())
				}
				return dialer.DialContext(ctx, network, addr)
			}

			// Hostname path (E-01): resolve at dial time and check every resolved
			// IP against the blocklist before connecting. We then dial the first
			// resolved IP as a literal string to avoid a second OS-level resolution
			// between the check and the connect (DNS-rebind window).
			if !policy.AllowPrivateHosts {
				ips, lookupErr := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
				if lookupErr != nil {
					return nil, fmt.Errorf("resolve host %q: %w", host, lookupErr)
				}
				for _, resolved := range ips {
					if isBlockedAddr(resolved) {
						return nil, fmt.Errorf("blocked address: %s (resolved from %q)", resolved.String(), host)
					}
				}
				if len(ips) > 0 {
					// Dial the first resolved IP as a literal to close the
					// TOCTOU window between resolution check and connect.
					literalAddr := net.JoinHostPort(ips[0].String(), port)
					return dialer.DialContext(ctx, network, literalAddr)
				}
			}

			return dialer.DialContext(ctx, network, addr)
		},
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("too many redirects")
			}
			return ValidateHTTPURL(req.Context(), req.URL.String(), policy.AllowPrivateHosts)
		},
	}
}

// NewSafeHTTPClient builds an HTTP client that blocks private addresses.
func NewSafeHTTPClient(timeout time.Duration) *http.Client {
	return NewHTTPClient(timeout, ClientPolicy{AllowPrivateHosts: false})
}

// ValidatePublicHTTPURL verifies URL syntax and blocks private hosts.
func ValidatePublicHTTPURL(ctx context.Context, raw string) error {
	return ValidateHTTPURL(ctx, raw, false)
}

// ValidateHTTPURL verifies URL syntax and optionally allows private hosts.
func ValidateHTTPURL(ctx context.Context, raw string, allowPrivateHosts bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid source url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported url scheme %q", u.Scheme)
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return fmt.Errorf("missing source host")
	}
	if !allowPrivateHosts && strings.EqualFold(host, "localhost") {
		return fmt.Errorf("blocked host %q", host)
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !allowPrivateHosts && isBlockedAddr(ip) {
			return fmt.Errorf("blocked address: %s", ip.String())
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("resolve host %q: %w", host, err)
	}
	if !allowPrivateHosts {
		for _, ip := range ips {
			if isBlockedAddr(ip) {
				return fmt.Errorf("blocked address: %s", ip.String())
			}
		}
	}
	return nil
}

func isBlockedAddr(ip netip.Addr) bool {
	if !ip.IsValid() {
		return true
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified()
}
