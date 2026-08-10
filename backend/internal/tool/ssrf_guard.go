package tool

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/huoguojun123/EffChat/internal/netpolicy"
)

// SSRF 防护：basic 爬虫在服务端直连任意 URL，若不设防可被诱导访问内网/环回/云元数据
// 端点（169.254.169.254）。这里在两道关卡拦截：
//   1. 请求前按 host 解析所有 IP 做白判（拒私网/环回/链路本地/唯一本地/未指定/组播）；
//   2. 实际拨号时复核 connaddr 的 IP，防 DNS rebind（解析时公网、拨号时被改私网）。
// firecrawl/jina 走外部 SaaS，在它们机房抓取，不经本防护。

// isBlockedIP 判断 IP 是否属于禁止直连的范围。
func isBlockedIP(ip net.IP) bool {
	return netpolicy.IsBlockedIP(ip)
}

// ipResolver 抽象 DNS 解析，*net.Resolver 天然满足；测试可注入伪解析模拟 rebind。
type ipResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// validatePublicURL 解析 URL host 并确保所有解析到的 IP 均按 blocked 策略放行。
// 返回错误即应拒绝抓取。blocked 默认应为 isBlockedIP，测试可注入放宽策略。
func validatePublicURL(ctx context.Context, resolver ipResolver, blocked func(net.IP) bool, pageURL string) error {
	parsed, err := url.Parse(pageURL)
	if err != nil {
		return fmt.Errorf("invalid url")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported scheme")
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("empty host")
	}
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return fmt.Errorf("blocked host")
	}

	// host 可能本身是 IP 字面量。
	if ip := net.ParseIP(host); ip != nil {
		if blocked(ip) {
			return fmt.Errorf("blocked address")
		}
		return nil
	}

	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("dns resolution failed: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("no addresses resolved")
	}
	for _, addr := range ips {
		if blocked(addr.IP) {
			return fmt.Errorf("blocked address")
		}
	}
	return nil
}

// validateURLLiteralHostPolicy 只检查 URL host 字面量，不做 DNS 解析。
// 第三方提取服务不应被本机代理 DNS/fake-ip 误杀，但 localhost 和显式内网 IP
// 仍属于明显危险输入，应在进入任何 crawler 前拒绝。
func validateURLLiteralHostPolicy(parsed *url.URL, blocked func(net.IP) bool) error {
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("empty host")
	}
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return fmt.Errorf("blocked host")
	}
	if ip := net.ParseIP(host); ip != nil && blocked(ip) {
		return fmt.Errorf("blocked address")
	}
	return nil
}

// newGuardedHTTPClient 构造一个拨号时按 blocked 策略复核目标 IP 的 http.Client，防 DNS rebind。
//
// 关键点：Go 默认把 hostname（非已解析 IP）交给 DialContext，若只在 addr 恰为数值 IP 时复核，
// hostname 路径会被底层 resolver 独立重解析、绕过校验——rebind 窗口仍在。这里在 DialContext 内
// 自行解析 host 的所有 IP 并逐个 blocked 校验，只对通过校验的数值 IP 发起拨号，绝不把 hostname
// 交回 dialer 重解析，从而真正闭合「解析时公网、拨号时私网」的 rebind 缺口。
func newGuardedHTTPClient(timeout time.Duration, resolver ipResolver, blocked func(net.IP) bool) *http.Client {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		// Basic requests gzip explicitly so the fetcher can enforce independent
		// compressed and decoded limits instead of accepting Transport's implicit
		// transparent decompression boundary.
		DisableCompression: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			// host 已是 IP 字面量：直接校验后拨号。
			if ip := net.ParseIP(host); ip != nil {
				if blocked(ip) {
					return nil, fmt.Errorf("blocked address")
				}
				return dialer.DialContext(ctx, network, addr)
			}
			// hostname：自行解析 + 逐个校验，只连通过校验的数值 IP（不把 hostname 交回 dialer）。
			ips, err := resolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("no addresses resolved")
			}
			for _, addr := range ips {
				if blocked(addr.IP) {
					return nil, fmt.Errorf("blocked address")
				}
			}
			var lastErr error
			for _, addr := range ips {
				conn, derr := dialer.DialContext(ctx, network, net.JoinHostPort(addr.IP.String(), port))
				if derr == nil {
					return conn, nil
				}
				lastErr = derr
			}
			return nil, lastErr
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}
