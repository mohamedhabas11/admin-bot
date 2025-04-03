package forwardproxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url" // Import url
	"time"
)

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
	// Add/Modify headers if needed (e.g., Via header)
	// outReq.Header.Add("Via", "admin-bot-proxy")

	// --- Configure Client to bypass proxy ---
	// Use a shared client? For now, create per request. Consider pooling later.
	client := &http.Client{
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
		// Prevent auto-following redirects if you want the proxy to handle them
		// CheckRedirect: func(req *http.Request, via []*http.Request) error {
		//  return http.ErrUseLastResponse
		// },
	}
	// --- End Client Configuration ---

	// Execute the request
	log.Printf("Fetching: %s %s", outReq.Method, outReq.URL)
	resp, err = client.Do(outReq)
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
		log.Printf("WARN: Failed to read response body from %s: %v", outReq.URL.Host, err)
		resp.Body.Close() // Close immediately if read failed
		// Return error because we can't cache or serve incomplete body
		return resp, nil, fmt.Errorf("failed to read response body: %w", err)
	}
	// VERY IMPORTANT: Replace the original resp.Body with a new reader based on
	// the bytes we just read, because the original reader is now drained.
	resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	return resp, bodyBytes, nil
}

// copyHeaders function needs to be accessible here if not moved to a utils package
// Ensure copyHeaders is defined either here or imported if moved.
// func copyHeaders(dst, src http.Header) { ... } // Definition is in proxy.go
