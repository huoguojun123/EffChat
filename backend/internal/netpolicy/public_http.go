package netpolicy

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
)

var blockedIPRanges = mustParseIPRanges("100.64.0.0/10", "198.18.0.0/15")

type IPResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// PublicHTTPResolution is the result of validating a URL and resolving its
// destination once. Callers that perform multiple network operations should
// retain this result so later operations cannot silently re-resolve the host.
type PublicHTTPResolution struct {
	Scheme    string
	Host      string
	Port      string
	Addresses []net.IPAddr
}

func IsBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	if ip.Equal(net.ParseIP("fd00:ec2::254")) {
		return true
	}
	for _, network := range blockedIPRanges {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func ResolvePublicHTTPURL(ctx context.Context, resolver IPResolver, raw string) (PublicHTTPResolution, error) {
	var resolution PublicHTTPResolution
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return resolution, fmt.Errorf("invalid url")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return resolution, fmt.Errorf("unsupported scheme")
	}
	if parsed.User != nil {
		return resolution, fmt.Errorf("url must not include credentials")
	}
	host := parsed.Hostname()
	if host == "" {
		return resolution, fmt.Errorf("empty host")
	}
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return resolution, fmt.Errorf("blocked host")
	}
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		if IsBlockedIP(ip) {
			return resolution, fmt.Errorf("blocked address")
		}
		return PublicHTTPResolution{Scheme: parsed.Scheme, Host: lower, Port: port, Addresses: []net.IPAddr{{IP: ip}}}, nil
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return resolution, fmt.Errorf("dns resolution failed: %w", err)
	}
	if len(ips) == 0 {
		return resolution, fmt.Errorf("no addresses resolved")
	}
	for _, addr := range ips {
		if IsBlockedIP(addr.IP) {
			return resolution, fmt.Errorf("blocked address")
		}
	}
	addresses := make([]net.IPAddr, len(ips))
	for i, addr := range ips {
		addresses[i] = net.IPAddr{IP: append(net.IP(nil), addr.IP...), Zone: addr.Zone}
	}
	return PublicHTTPResolution{Scheme: parsed.Scheme, Host: lower, Port: port, Addresses: addresses}, nil
}

func mustParseIPRanges(cidrs ...string) []*net.IPNet {
	ranges := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err)
		}
		ranges = append(ranges, network)
	}
	return ranges
}
