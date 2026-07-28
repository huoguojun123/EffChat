package tool

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsBlockedIP(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},       // loopback
		{"10.1.2.3", true},        // private A
		{"172.16.5.5", true},      // private B
		{"192.168.1.1", true},     // private C
		{"100.64.0.1", true},      // CGNAT / Tailscale
		{"100.127.255.254", true}, // CGNAT upper bound
		{"169.254.169.254", true}, // link-local / cloud metadata
		{"198.18.0.1", true},      // benchmarking range
		{"198.19.255.254", true},  // benchmarking upper bound
		{"0.0.0.0", true},         // unspecified
		{"::1", true},             // ipv6 loopback
		{"fc00::1", true},         // ipv6 unique-local (private)
		{"fe80::1", true},         // ipv6 link-local
		{"8.8.8.8", false},        // public
		{"1.1.1.1", false},        // public
		{"93.184.216.34", false},  // public (example.com)
	}
	for _, c := range cases {
		ip := net.ParseIP(c.ip)
		if got := isBlockedIP(ip); got != c.blocked {
			t.Errorf("isBlockedIP(%s) = %v, want %v", c.ip, got, c.blocked)
		}
	}
	if !isBlockedIP(nil) {
		t.Error("nil IP should be blocked")
	}
}

func TestValidatePublicURL_BlocksUnsafe(t *testing.T) {
	ctx := context.Background()
	r := net.DefaultResolver
	blocked := []string{
		"http://localhost/admin",
		"http://localhost:8080/",
		"http://service.localhost/",
		"http://127.0.0.1/",
		"http://10.0.0.5/internal",
		"http://192.168.0.1/",
		"http://100.64.0.1/",
		"http://198.18.0.1/",
		"http://169.254.169.254/latest/meta-data/", // cloud metadata
		"http://[::1]/",
		"ftp://example.com/", // unsupported scheme
		"file:///etc/passwd", // unsupported scheme
	}
	for _, u := range blocked {
		if err := validatePublicURL(ctx, r, isBlockedIP, u); err == nil {
			t.Errorf("validatePublicURL(%q) = nil, want blocked", u)
		}
	}
}

func TestValidatePublicURL_AllowsPublicIPLiteral(t *testing.T) {
	ctx := context.Background()
	r := net.DefaultResolver
	// 公网 IP 字面量不触发 DNS，应放行。
	for _, u := range []string{"http://8.8.8.8/", "https://1.1.1.1/"} {
		if err := validatePublicURL(ctx, r, isBlockedIP, u); err != nil {
			t.Errorf("validatePublicURL(%q) = %v, want allowed", u, err)
		}
	}
}

// rebindResolver 模拟 DNS rebind：第一次解析（前置校验）返回公网 IP，
// 之后的解析（拨号时复核）返回私网 IP。
type rebindResolver struct {
	calls   int32
	public  net.IP
	private net.IP
}

func (r *rebindResolver) LookupIPAddr(_ context.Context, _ string) ([]net.IPAddr, error) {
	if atomic.AddInt32(&r.calls, 1) == 1 {
		return []net.IPAddr{{IP: r.public}}, nil
	}
	return []net.IPAddr{{IP: r.private}}, nil
}

// 回归测试：前置校验解析为公网通过，但拨号时 host 被 rebind 到私网，
// DialContext 内自行解析 + 复核必须拦下，绝不把 hostname 交回底层 dialer。
func TestGuardedClient_BlocksDNSRebindAtDial(t *testing.T) {
	resolver := &rebindResolver{
		public:  net.ParseIP("93.184.216.34"),   // 公网
		private: net.ParseIP("169.254.169.254"), // 云元数据
	}

	// 前置校验用同一 resolver：第一次解析为公网 → 通过。
	if err := validatePublicURL(context.Background(), resolver, isBlockedIP, "http://rebind.test/"); err != nil {
		t.Fatalf("pre-check should pass on public resolution, got %v", err)
	}

	// 拨号阶段：resolver 已 rebind 到私网 → DialContext 必须拒绝。
	client := newGuardedHTTPClient(2*time.Second, resolver, isBlockedIP)
	req, _ := http.NewRequest(http.MethodGet, "http://rebind.test/", nil)
	resp, err := client.Do(req)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected dial to be blocked after rebind to private IP, got nil error")
	}
}
