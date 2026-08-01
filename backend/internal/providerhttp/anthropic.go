// Package providerhttp owns HTTP behavior that must be consistent across all
// constructors for one provider SDK.
package providerhttp

import (
	"io"
	"net/http"
	"strings"
)

const anthropicTransportErrorHeader = "X-EffChat-Transport-Error"

// NewAnthropicSingleAttemptClient prevents the Anthropic SDK from performing
// retries below EffChat's model lifecycle.
//
// The SDK retries selected HTTP statuses and connection errors by default. If
// those retries remain hidden inside Eino, ADK cannot emit model_retry, apply
// the product's one-retry cap, or record each upstream attempt consistently.
// Status responses opt out through Anthropic's documented x-should-retry
// header. Pre-response transport failures are represented as a private 599
// response so the SDK returns immediately and the shared EffChat classifier
// can preserve the connection category before ADK decides whether to retry.
func NewAnthropicSingleAttemptClient(base http.RoundTripper) *http.Client {
	if base == nil {
		base = http.DefaultTransport
	}
	return &http.Client{Transport: anthropicSingleAttemptTransport{base: base}}
}

// IsAnthropicTransportError identifies the private synthetic response without
// exposing the underlying network error or local endpoint in user-visible
// diagnostics.
func IsAnthropicTransportError(header http.Header) bool {
	return header.Get(anthropicTransportErrorHeader) == "1"
}

type anthropicSingleAttemptTransport struct {
	base http.RoundTripper
}

func (t anthropicSingleAttemptTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(req)
	if err == nil {
		if response.Header == nil {
			response.Header = make(http.Header)
		}
		response.Header.Set("x-should-retry", "false")
		return response, nil
	}
	if req.Context().Err() != nil {
		return nil, err
	}

	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	header.Set("x-should-retry", "false")
	header.Set(anthropicTransportErrorHeader, "1")
	return &http.Response{
		StatusCode: 599,
		Status:     "599 Provider Transport Error",
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"api_error","message":"provider transport failed"}}`)),
		Request:    req,
	}, nil
}
