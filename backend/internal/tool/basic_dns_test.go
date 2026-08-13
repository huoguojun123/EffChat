package tool

import (
	"context"
	"errors"
	"net"
	"testing"
)

type recordingResolver struct {
	ips   []net.IPAddr
	err   error
	calls int
}

func (r *recordingResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	r.calls++
	return r.ips, r.err
}

func TestSyntheticFallbackResolverUsesFallbackOnlyForAllSynthetic(t *testing.T) {
	tests := []struct {
		name          string
		primary       []string
		primaryErr    error
		wantFallback  bool
		wantFirstIP   string
		wantLookupErr bool
	}{
		{name: "all synthetic", primary: []string{"198.18.0.20", "198.19.0.30"}, wantFallback: true, wantFirstIP: "93.184.216.34"},
		{name: "mixed public and synthetic", primary: []string{"198.18.0.20", "93.184.216.34"}, wantFirstIP: "198.18.0.20"},
		{name: "real private", primary: []string{"10.0.0.8"}, wantFirstIP: "10.0.0.8"},
		{name: "ordinary dns error", primaryErr: errors.New("temporary dns failure"), wantLookupErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			primary := &recordingResolver{ips: ipAddresses(tt.primary), err: tt.primaryErr}
			fallback := &recordingResolver{ips: ipAddresses([]string{"93.184.216.34"})}
			resolver := syntheticFallbackResolver{primary: primary, fallback: fallback}
			ips, err := resolver.LookupIPAddr(context.Background(), "fixture.example")
			if tt.wantLookupErr != (err != nil) {
				t.Fatalf("LookupIPAddr() error = %v", err)
			}
			if (fallback.calls > 0) != tt.wantFallback {
				t.Fatalf("fallback calls = %d, wantFallback=%t", fallback.calls, tt.wantFallback)
			}
			if tt.wantFirstIP != "" && (len(ips) == 0 || ips[0].IP.String() != tt.wantFirstIP) {
				t.Fatalf("resolved = %#v, want first %s", ips, tt.wantFirstIP)
			}
		})
	}
}

func TestSyntheticFallbackResolverDoesNotBypassPrivateFallbackAnswer(t *testing.T) {
	resolver := syntheticFallbackResolver{
		primary:  &recordingResolver{ips: ipAddresses([]string{"198.18.0.20"})},
		fallback: &recordingResolver{ips: ipAddresses([]string{"169.254.169.254"})},
	}
	if err := validatePublicURL(context.Background(), resolver, isBlockedIP, "https://fixture.example/article"); err == nil {
		t.Fatal("private fallback answer must remain blocked")
	}
}

func ipAddresses(raw []string) []net.IPAddr {
	addresses := make([]net.IPAddr, 0, len(raw))
	for _, item := range raw {
		addresses = append(addresses, net.IPAddr{IP: net.ParseIP(item)})
	}
	return addresses
}
