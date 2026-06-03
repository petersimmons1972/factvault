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

type ClientPolicy struct {
	AllowPrivateHosts bool
}

func NewHTTPClient(timeout time.Duration, policy ClientPolicy) *http.Client {
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if ip, err := netip.ParseAddr(host); err == nil && !policy.AllowPrivateHosts && isBlockedAddr(ip) {
				return nil, fmt.Errorf("blocked address: %s", ip.String())
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

func NewSafeHTTPClient(timeout time.Duration) *http.Client {
	return NewHTTPClient(timeout, ClientPolicy{AllowPrivateHosts: false})
}

func ValidatePublicHTTPURL(ctx context.Context, raw string) error {
	return ValidateHTTPURL(ctx, raw, false)
}

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
