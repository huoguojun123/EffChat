package tool

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

var syntheticDNSRange = mustParseCIDR("198.18.0.0/15")

// syntheticFallbackResolver keeps the deployment's normal resolver authoritative.
// It only uses an independent public resolver when every primary answer belongs to
// the RFC 2544 benchmarking range used by fake-IP proxy modes. A real private or
// mixed answer is returned unchanged so the existing SSRF policy rejects it; DNS
// errors also remain errors instead of being silently bypassed.
type syntheticFallbackResolver struct {
	primary  ipResolver
	fallback ipResolver
}

func newBasicResolver() ipResolver {
	return syntheticFallbackResolver{
		primary:  net.DefaultResolver,
		fallback: newPublicDNSResolver(),
	}
}

func (r syntheticFallbackResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	ips, err := r.primary.LookupIPAddr(ctx, host)
	if err != nil || !allSyntheticDNSAnswers(ips) {
		return ips, err
	}
	log.Printf("[web_extract] synthetic_dns_fallback host_chars=%d", toolLogRuneCount(host))
	return r.fallback.LookupIPAddr(ctx, host)
}

func allSyntheticDNSAnswers(ips []net.IPAddr) bool {
	if len(ips) == 0 {
		return false
	}
	for _, addr := range ips {
		if addr.IP == nil || !syntheticDNSRange.Contains(addr.IP) {
			return false
		}
	}
	return true
}

func newPublicDNSResolver() ipResolver {
	return publicDNSResolver{servers: []string{"8.8.8.8:53", "1.1.1.1:53"}}
}

type publicDNSResolver struct {
	servers []string
}

func (r publicDNSResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	var failures []error
	for _, server := range r.servers {
		resolver := resolverForDNSServer(server)
		queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		ips, err := resolver.LookupIPAddr(queryCtx, host)
		cancel()
		if err == nil && len(ips) > 0 {
			return ips, nil
		}
		if err == nil {
			err = fmt.Errorf("no addresses resolved")
		}
		failures = append(failures, fmt.Errorf("%s: %w", server, err))
	}
	return nil, fmt.Errorf("public dns unavailable: %w", errors.Join(failures...))
}

func resolverForDNSServer(server string) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			dialNetwork := "udp"
			if strings.HasPrefix(network, "tcp") {
				dialNetwork = "tcp"
			}
			dialer := net.Dialer{Timeout: 2 * time.Second}
			return dialer.DialContext(ctx, dialNetwork, server)
		},
	}
}

func mustParseCIDR(raw string) *net.IPNet {
	_, network, err := net.ParseCIDR(raw)
	if err != nil {
		panic(err)
	}
	return network
}
