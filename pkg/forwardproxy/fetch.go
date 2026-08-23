package forwardproxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url" // Import url
	"time"
)

// proxyHTTPClient is shared by all outgoing proxied requests so connections
// are pooled and reused across requests instead of being re-established each time.
var proxyHTTPClient = newProxyHTTPClient()

// newProxyHTTPClient builds the client used for origin fetches.
// Proxy use is explicitly disabled so proxied traffic cannot loop back
// through this process or an ambient environment proxy.
func newProxyHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second, // Overall request timeout
		Transport: &http.Transport{
			Proxy: nil, // Explicitly disable proxy use for this client
			// Copy settings from http.DefaultTransport for robustness
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second, // Connection timeout
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

// PerformFetch executes the outgoing HTTP request.
func PerformFetch(origReq *http.Request) (resp *http.Response, bodyBytes []byte, err error) {
	// Create a new request based on the original request to avoid modifying it.
	// The URL should already be absolute from HandleHTTP.
	// Pass the original request's context to the new request.
	outReq, err := http.NewRequestWithContext(origReq.Context(), origReq.Method, origReq.URL.String(), origReq.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create outgoing request: %w", err)
	}

	// Copy headers, filtering hop-by-hop headers
	copyHeaders(outReq.Header, origReq.Header)
	// Remove proxy-specific headers from outgoing request
	outReq.Header.Del("Proxy-Connection")
	outReq.Header.Del("Proxy-Authorization")
	// Execute the request on the shared client
	slog.Debug("origin fetch", "method", outReq.Method, "url", outReq.URL)
	resp, err = proxyHTTPClient.Do(outReq)
	if err != nil {
		// Check specifically for context deadline exceeded which indicates timeout
		// Use errors.Is for robust error checking
		// Need to check url.Error as client.Do wraps errors
		var urlErr *url.Error
		if errors.As(err, &urlErr) && errors.Is(urlErr.Err, context.DeadlineExceeded) {
			return nil, nil, fmt.Errorf("failed to execute outgoing request to %s: timeout exceeded: %w", outReq.URL.Host, err)
		}
		return nil, nil, fmt.Errorf("failed to execute outgoing request to %s: %w", outReq.URL.Host, err)
	}
	// Note: resp.Body will be closed by the caller (HandleHTTP or ServeFromCacheOrFetch)

	// Read the body bytes for caching purposes
	bodyBytes, err = io.ReadAll(resp.Body)
	if err != nil {
		slog.Warn("failed to read response body", "host", outReq.URL.Host, "err", err)
		resp.Body.Close() // Close immediately if read failed
		// Return error because we can't cache or serve incomplete body
		return resp, nil, fmt.Errorf("failed to read response body: %w", err)
	}
	// VERY IMPORTANT: Replace the original resp.Body with a new reader based on
	// the bytes we just read, because the original reader is now drained.
	resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	return resp, bodyBytes, nil
}
