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

func ValidatePublicHTTPURL(ctx context.Context, resolver IPResolver, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("invalid url")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported scheme")
	}
	if parsed.User != nil {
		return fmt.Errorf("url must not include credentials")
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("empty host")
	}
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return fmt.Errorf("blocked host")
	}
	if ip := net.ParseIP(host); ip != nil {
		if IsBlockedIP(ip) {
			return fmt.Errorf("blocked address")
		}
		return nil
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("dns resolution failed: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("no addresses resolved")
	}
	for _, addr := range ips {
		if IsBlockedIP(addr.IP) {
			return fmt.Errorf("blocked address")
		}
	}
	return nil
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
