package netpolicy

import (
	"context"
	"net"
	"testing"
)

type fixedResolver struct {
	addresses []net.IPAddr
}

func (r fixedResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return r.addresses, nil
}

func TestResolvePublicHTTPURLReturnsTheValidatedDestinationSet(t *testing.T) {
	resolverAddresses := []net.IPAddr{
		{IP: net.ParseIP("93.184.216.34")},
		{IP: net.ParseIP("2001:db8::34")},
	}
	resolution, err := ResolvePublicHTTPURL(context.Background(), fixedResolver{addresses: resolverAddresses}, "https://Git.Example/repo.git")
	if err != nil {
		t.Fatalf("resolve public URL: %v", err)
	}
	if resolution.Scheme != "https" || resolution.Host != "git.example" || resolution.Port != "443" {
		t.Fatalf("unexpected normalized destination: %#v", resolution)
	}
	if len(resolution.Addresses) != 2 || !resolution.Addresses[0].IP.Equal(resolverAddresses[0].IP) || !resolution.Addresses[1].IP.Equal(resolverAddresses[1].IP) {
		t.Fatalf("unexpected validated addresses: %#v", resolution.Addresses)
	}

	resolverAddresses[0].IP[0] ^= 0xff
	if !resolution.Addresses[0].IP.Equal(net.ParseIP("93.184.216.34")) {
		t.Fatal("resolution retained mutable resolver-owned address storage")
	}
}

func TestResolvePublicHTTPURLRejectsMixedPublicAndBlockedAnswers(t *testing.T) {
	_, err := ResolvePublicHTTPURL(context.Background(), fixedResolver{addresses: []net.IPAddr{
		{IP: net.ParseIP("93.184.216.34")},
		{IP: net.ParseIP("127.0.0.1")},
	}}, "https://git.example/repo.git")
	if err == nil {
		t.Fatal("expected a mixed public and blocked DNS answer to be rejected")
	}
}
