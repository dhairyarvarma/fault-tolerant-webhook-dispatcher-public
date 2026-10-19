// Package outbound applies the destination policy shared by target
// registration and outbound webhook delivery.
package outbound

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var ErrDisallowedDestination = errors.New("destination is not permitted by outbound policy")

// Conservative public-webhook policy; see SECURITY_MODEL.md and Decision 37.
var specialPurpose = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("3fff::/20"),
}
var supportedIPv6 = netip.MustParsePrefix("2000::/3")

// Resolver is the subset of net.Resolver used by Policy. It keeps address
// classification deterministic in tests.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Policy validates webhook destinations. Private destinations are allowed
// only in explicit local-development mode.
type Policy struct {
	AllowPrivate   bool
	Resolver       Resolver
	ConnectTimeout time.Duration
}

func NewPolicy(allowPrivate bool) Policy {
	return Policy{AllowPrivate: allowPrivate, Resolver: net.DefaultResolver}
}

// ValidateURL checks the URL shape and every address currently resolved for
// its host. The actual dialer repeats this check immediately before connecting
// to protect against DNS changes after target registration.
func (p Policy) ValidateURL(ctx context.Context, rawURL string) (*url.URL, error) {
	u, err := url.ParseRequestURI(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, ErrDisallowedDestination
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.User != nil || u.Hostname() == "" {
		return nil, ErrDisallowedDestination
	}
	if _, err := p.resolveAllowed(ctx, u.Hostname()); err != nil {
		return nil, err
	}
	return u, nil
}

// DialContext resolves the hostname itself and dials a validated IP address,
// preventing the standard transport from connecting to a private address after
// DNS changes between validation and delivery.
func (p Policy) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, ErrDisallowedDestination
	}
	addresses, err := p.resolveAllowed(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: p.ConnectTimeout}
	var lastErr error
	for _, addr := range addresses {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, fmt.Errorf("dial approved destination: %w", lastErr)
	}
	return nil, ErrDisallowedDestination
}

func (p Policy) resolveAllowed(ctx context.Context, host string) ([]netip.Addr, error) {
	resolver := p.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addresses, err := resolver.LookupNetIP(ctx, "ip", strings.Trim(host, "[]"))
	if err != nil {
		return nil, fmt.Errorf("resolve destination: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("destination resolved to no addresses")
	}
	if p.AllowPrivate {
		return addresses, nil
	}
	for _, addr := range addresses {
		if !isPublic(addr) {
			return nil, ErrDisallowedDestination
		}
	}
	return addresses, nil
}

func isPublic(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	if addr.Zone() != "" || (addr.Is6() && !supportedIPv6.Contains(addr)) {
		return false
	}
	for _, prefix := range specialPurpose {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}
