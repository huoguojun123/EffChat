package providerhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestAnthropicSingleAttemptClientDisablesSDKStatusRetry(t *testing.T) {
	client := NewAnthropicSingleAttemptClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"type":"error"}`)),
			Request:    req,
		}, nil
	}))

	response, err := client.Get("https://provider.invalid/v1/messages")
	if err != nil {
		t.Fatalf("GET returned error: %v", err)
	}
	defer response.Body.Close()
	if response.Header.Get("x-should-retry") != "false" {
		t.Fatalf("x-should-retry = %q, want false", response.Header.Get("x-should-retry"))
	}
	if IsAnthropicTransportError(response.Header) {
		t.Fatal("ordinary upstream status was marked as transport error")
	}
}

func TestAnthropicSingleAttemptClientTurnsTransportFailureIntoPrivateResponse(t *testing.T) {
	upstreamErr := errors.New("dial failed with private endpoint")
	client := NewAnthropicSingleAttemptClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, upstreamErr
	}))

	response, err := client.Get("https://provider.invalid/v1/messages")
	if err != nil {
		t.Fatalf("GET returned error: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != 599 || !IsAnthropicTransportError(response.Header) {
		t.Fatalf("synthetic response = status:%d headers:%v", response.StatusCode, response.Header)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read synthetic body: %v", err)
	}
	if strings.Contains(string(body), "private endpoint") || strings.Contains(string(body), upstreamErr.Error()) {
		t.Fatalf("synthetic body leaked transport detail: %s", body)
	}
}

func TestAnthropicSingleAttemptClientPreservesContextCancellation(t *testing.T) {
	upstreamErr := errors.New("canceled transport")
	client := NewAnthropicSingleAttemptClient(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, upstreamErr
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://provider.invalid/v1/messages", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(req); err == nil {
		t.Fatal("canceled request unexpectedly became a synthetic retryable response")
	}
}
