package outbound

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

type fakeResolver map[string][]netip.Addr

func (r fakeResolver) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	addresses, ok := r[host]
	if !ok {
		return nil, errors.New("host not found")
	}
	return addresses, nil
}

func TestPolicyValidateURL_RejectsPrivateAndUnsafeDestinationsInPublicMode(t *testing.T) {
	policy := NewPolicy(false)
	policy.Resolver = fakeResolver{
		"private.test": {netip.MustParseAddr("127.0.0.1")},
		"mixed.test":   {netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("10.0.0.1")},
		"public.test":  {netip.MustParseAddr("8.8.8.8")},
	}

	for _, rawURL := range []string{
		"http://private.test/webhook",
		"https://mixed.test/webhook",
		"ftp://public.test/webhook",
		"https://user:secret@public.test/webhook",
	} {
		if _, err := policy.ValidateURL(t.Context(), rawURL); !errors.Is(err, ErrDisallowedDestination) {
			t.Errorf("ValidateURL(%q) error = %v, want ErrDisallowedDestination", rawURL, err)
		}
	}
}

func TestPolicyValidateURL_AllowsPublicAndExplicitLocalDestinations(t *testing.T) {
	resolver := fakeResolver{
		"public.test":  {netip.MustParseAddr("8.8.8.8")},
		"private.test": {netip.MustParseAddr("127.0.0.1")},
	}

	public := NewPolicy(false)
	public.Resolver = resolver
	if _, err := public.ValidateURL(t.Context(), "https://public.test/webhook"); err != nil {
		t.Fatalf("public destination rejected: %v", err)
	}

	local := NewPolicy(true)
	local.Resolver = resolver
	if _, err := local.ValidateURL(t.Context(), "http://private.test/webhook"); err != nil {
		t.Fatalf("explicit local destination rejected: %v", err)
	}
}

func TestPolicyRejectsMappedPrivateAndCGNATAddresses(t *testing.T) {
	for _, raw := range []string{"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:100.64.0.1"} {
		if isPublic(netip.MustParseAddr(raw)) {
			t.Errorf("accepted %s", raw)
		}
	}
	p := NewPolicy(false)
	p.Resolver = fakeResolver{}
	if _, err := p.ValidateURL(t.Context(), "https://missing.test/webhook"); err == nil || errors.Is(err, ErrDisallowedDestination) {
		t.Fatalf("DNS failure should remain a dependency error: %v", err)
	}
}

func TestPublicPolicyRejectsSpecialAndTranslationRanges(t *testing.T) {
	for _, raw := range []string{"0.1.2.3", "192.0.0.9", "192.0.2.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "240.0.0.1", "::ffff:198.18.0.1", "64:ff9b::a00:1", "64:ff9b:1::1", "100::1", "2001:db8::1", "2002:a00:1::1", "3fff::1", "5f00::1"} {
		if isPublic(netip.MustParseAddr(raw)) {
			t.Errorf("accepted special address %s", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2001:4860:4860::8888", "2606:4700:4700::1111"} {
		if !isPublic(netip.MustParseAddr(raw)) {
			t.Errorf("rejected public address %s", raw)
		}
	}
}
