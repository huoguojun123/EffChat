package service

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestGitTransportPinsValidatedAddressesIntoGitCommand(t *testing.T) {
	resolver := fixedSkillResolver{ips: []net.IPAddr{
		{IP: net.ParseIP("93.184.216.34")},
		{IP: net.ParseIP("2001:db8::34")},
		{IP: net.ParseIP("93.184.216.34")},
	}}
	plan, err := resolveGitTransportWithResolver(context.Background(), resolver, "https://git.example/skill.git")
	if err != nil {
		t.Fatalf("resolve git transport: %v", err)
	}
	cmd := gitCommand(context.Background(), plan, "ls-remote", "https://git.example/skill.git", "HEAD")
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "http.curloptResolve=git.example:443:93.184.216.34,[2001:db8::34]") {
		t.Fatalf("validated addresses were not pinned: args=%q", cmd.Args)
	}
	for _, required := range []string{"http.followRedirects=false", "http.proxy="} {
		if !strings.Contains(joined, required) {
			t.Fatalf("required transport control missing: %q", required)
		}
	}
}

func TestGitTransportTrustedHostExceptionIsExact(t *testing.T) {
	resolver := fixedSkillResolver{ips: []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}}
	trusted, err := resolveGitTransportWithResolver(context.Background(), resolver, "https://github.com/example/skill.git")
	if err != nil {
		t.Fatalf("resolve trusted host: %v", err)
	}
	if trusted.pinDNS {
		t.Fatal("trusted forge host should retain provider-owned CDN rotation")
	}
	lookalike, err := resolveGitTransportWithResolver(context.Background(), resolver, "https://mirror.github.com/example/skill.git")
	if err != nil {
		t.Fatalf("resolve lookalike host: %v", err)
	}
	if !lookalike.pinDNS || lookalike.host != "mirror.github.com" {
		t.Fatalf("lookalike host inherited trusted exception: %#v", lookalike)
	}
}

func TestSanitizedGitEnvironmentRejectsInheritedTransportConfiguration(t *testing.T) {
	env := sanitizedGitEnvironment([]string{
		"HTTP_PROXY=http://proxy.invalid:8080",
		"https_proxy=http://proxy.invalid:8080",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=url.https://evil.invalid/.insteadOf",
		"GIT_CONFIG_VALUE_0=https://git.example/",
		"GIT_SSH_COMMAND=ssh -o ProxyCommand=evil",
		"GIT_SSL_NO_VERIFY=true",
		"PATH=/usr/bin",
	})
	joined := strings.Join(env, "\n")
	for _, forbidden := range []string{"proxy.invalid", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "GIT_SSH_COMMAND", "GIT_SSL_NO_VERIFY"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("inherited transport setting survived sanitization: %q", forbidden)
		}
	}
	for _, required := range []string{"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_GLOBAL=/dev/null"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("required git environment setting missing: %q", required)
		}
	}
}
