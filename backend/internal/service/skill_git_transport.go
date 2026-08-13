package service

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/huoguojun123/EffChat/internal/netpolicy"
)

var trustedGitHosts = map[string]struct{}{
	"github.com":    {},
	"gitlab.com":    {},
	"bitbucket.org": {},
	"codeberg.org":  {},
}

type gitTransportPlan struct {
	host      string
	port      string
	addresses []net.IPAddr
	pinDNS    bool
}

// gitCommand applies the same immutable transport policy to the initial clone
// and every later command that may lazily fetch blobs from the partial clone.
// Keeping the original HTTPS hostname preserves TLS SNI and certificate checks;
// curloptResolve only fixes the peer addresses validated before Git started.
func gitCommand(ctx context.Context, transport gitTransportPlan, args ...string) *exec.Cmd {
	commandArgs := []string{
		"-c", "http.followRedirects=false",
		"-c", "http.proxy=",
	}
	if transport.pinDNS {
		commandArgs = append(commandArgs, "-c", "http.curloptResolve="+curlResolveValue(transport))
	}
	commandArgs = append(commandArgs, args...)
	cmd := exec.CommandContext(ctx, "git", commandArgs...)
	cmd.Env = sanitizedGitEnvironment(os.Environ())
	return cmd
}

func curlResolveValue(transport gitTransportPlan) string {
	seen := make(map[string]struct{}, len(transport.addresses))
	addresses := make([]string, 0, len(transport.addresses))
	for _, address := range transport.addresses {
		ip := address.IP
		if ip == nil {
			continue
		}
		value := ip.String()
		if ip.To4() == nil {
			value = "[" + value + "]"
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		addresses = append(addresses, value)
	}
	return transport.host + ":" + transport.port + ":" + strings.Join(addresses, ",")
}

func sanitizedGitEnvironment(environ []string) []string {
	blocked := map[string]struct{}{
		"ALL_PROXY": {}, "HTTP_PROXY": {}, "HTTPS_PROXY": {}, "NO_PROXY": {},
		"SSH_ASKPASS": {},
	}
	clean := make([]string, 0, len(environ)+4)
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		if _, denied := blocked[upper]; denied || strings.HasPrefix(upper, "GIT_") {
			continue
		}
		clean = append(clean, entry)
	}
	return append(clean,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_GLOBAL=/dev/null",
	)
}

func validateGitURL(ctx context.Context, raw string) error {
	_, err := resolveGitTransportWithResolver(ctx, net.DefaultResolver, raw)
	return err
}

func validateGitURLWithResolver(ctx context.Context, resolver netpolicy.IPResolver, raw string) error {
	_, err := resolveGitTransportWithResolver(ctx, resolver, raw)
	return err
}

func resolveGitTransport(ctx context.Context, raw string) (gitTransportPlan, error) {
	return resolveGitTransportWithResolver(ctx, net.DefaultResolver, raw)
}

func resolveGitTransportWithResolver(ctx context.Context, resolver netpolicy.IPResolver, raw string) (gitTransportPlan, error) {
	var transport gitTransportPlan
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Scheme != "https" {
		return transport, fmt.Errorf("invalid git url")
	}
	if parsed.User != nil {
		return transport, fmt.Errorf("invalid git url: url must not include credentials")
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return transport, fmt.Errorf("invalid git url: unsupported port")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return transport, fmt.Errorf("invalid git url: blocked host")
	}
	if ip := net.ParseIP(host); ip != nil {
		if netpolicy.IsBlockedIP(ip) {
			return transport, fmt.Errorf("invalid git url: blocked address")
		}
		return gitTransportPlan{host: host, port: "443"}, nil
	}
	if _, trusted := trustedGitHosts[host]; trusted {
		// These fixed public forge hosts are an explicit operational exception:
		// their CDN address rotation remains owned by the provider. Redirects,
		// credentials, proxies, interactive auth, and Git config injection are
		// still disabled by gitCommand.
		return gitTransportPlan{host: host, port: "443"}, nil
	}
	resolution, err := netpolicy.ResolvePublicHTTPURL(ctx, resolver, raw)
	if err != nil {
		return transport, fmt.Errorf("invalid git url: %w", err)
	}
	return gitTransportPlan{
		host:      resolution.Host,
		port:      resolution.Port,
		addresses: resolution.Addresses,
		pinDNS:    true,
	}, nil
}
