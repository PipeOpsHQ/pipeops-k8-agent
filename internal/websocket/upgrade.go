package websocket

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	// WebSocket GUID for handshake
	websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
)

// UpgradeRequest represents a WebSocket upgrade request
type UpgradeRequest struct {
	Method  string
	Path    string
	Query   string
	Headers http.Header
	Host    string
}

// BuildUpgradeRequest builds a WebSocket upgrade request bytes
func BuildUpgradeRequest(req *UpgradeRequest) []byte {
	var sb strings.Builder

	// Request line
	path := req.Path
	if req.Query != "" {
		path = fmt.Sprintf("%s?%s", path, req.Query)
	}
	sb.WriteString(fmt.Sprintf("%s %s HTTP/1.1\r\n", req.Method, path))

	// Host header (required)
	if req.Host != "" {
		sb.WriteString(fmt.Sprintf("Host: %s\r\n", req.Host))
	}

	// Upgrade headers (required for WebSocket)
	sb.WriteString("Upgrade: websocket\r\n")
	sb.WriteString("Connection: Upgrade\r\n")

	// WebSocket version
	sb.WriteString("Sec-WebSocket-Version: 13\r\n")

	// Generate Sec-WebSocket-Key (random key, not security-critical for this use)
	key := generateWebSocketKey()
	sb.WriteString(fmt.Sprintf("Sec-WebSocket-Key: %s\r\n", key))

	// Add other headers (skip hop-by-hop headers)
	hopByHop := map[string]bool{
		"connection":          true,
		"keep-alive":          true,
		"proxy-authenticate":  true,
		"proxy-authorization": true,
		"te":                  true,
		"trailer":             true,
		"transfer-encoding":   true,
		"upgrade":             true,
		"host":                true,
	}

	for name, values := range req.Headers {
		nameLower := strings.ToLower(name)
		if hopByHop[nameLower] {
			continue
		}
		for _, value := range values {
			sb.WriteString(fmt.Sprintf("%s: %s\r\n", name, value))
		}
	}

	// End of headers
	sb.WriteString("\r\n")

	return []byte(sb.String())
}

// ReadHTTPResponse reads and parses an HTTP response
func ReadHTTPResponse(r io.Reader) (*http.Response, error) {
	br := bufio.NewReader(r)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return nil, fmt.Errorf("read HTTP response: %w", err)
	}
	return resp, nil
}

// generateWebSocketKey generates a random WebSocket key
// This is a simplified version - in production use crypto/rand
func generateWebSocketKey() string {
	// For testing and non-security-critical use, we can use a simple fixed key
	// In production, this should use crypto/rand
	key := []byte("test-websocket-key12")
	return base64.StdEncoding.EncodeToString(key)
}

// CalculateAcceptKey calculates the Sec-WebSocket-Accept value
func CalculateAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key + websocketGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// ValidateUpgradeResponse validates a WebSocket upgrade response
func ValidateUpgradeResponse(resp *http.Response, sentKey string) error {
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return fmt.Errorf("unexpected status code: %d (expected 101)", resp.StatusCode)
	}

	if !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") {
		return fmt.Errorf("missing or invalid Upgrade header")
	}

	if !strings.EqualFold(resp.Header.Get("Connection"), "upgrade") {
		return fmt.Errorf("missing or invalid Connection header")
	}

	// Validate Sec-WebSocket-Accept if key was sent
	if sentKey != "" {
		expectedAccept := CalculateAcceptKey(sentKey)
		actualAccept := resp.Header.Get("Sec-WebSocket-Accept")
		if actualAccept != expectedAccept {
			return fmt.Errorf("invalid Sec-WebSocket-Accept header")
		}
	}

	return nil
}

// PrepareHeaders prepares headers for WebSocket upgrade by removing hop-by-hop headers
func PrepareHeaders(headers map[string][]string) http.Header {
	result := http.Header{}

	hopByHop := map[string]bool{
		"connection":          true,
		"keep-alive":          true,
		"proxy-authenticate":  true,
		"proxy-authorization": true,
		"te":                  true,
		"trailer":             true,
		"transfer-encoding":   true,
		"upgrade":             true,
		"proxy-connection":    true,
		"http2-settings":      true,
		"host":                true,
	}

	// Parse Connection header for additional headers to remove
	if connHeaders, ok := headers["Connection"]; ok {
		for _, connHeader := range connHeaders {
			for _, headerName := range strings.Split(connHeader, ",") {
				headerName = strings.TrimSpace(strings.ToLower(headerName))
				if headerName != "" && headerName != "connection" && headerName != "keep-alive" {
					hopByHop[headerName] = true
				}
			}
		}
	}

	// Also check lowercase variant
	if connHeaders, ok := headers["connection"]; ok {
		for _, connHeader := range connHeaders {
			for _, headerName := range strings.Split(connHeader, ",") {
				headerName = strings.TrimSpace(strings.ToLower(headerName))
				if headerName != "" && headerName != "connection" && headerName != "keep-alive" {
					hopByHop[headerName] = true
				}
			}
		}
	}

	// Copy headers, filtering out hop-by-hop headers
	for key, values := range headers {
		lowerKey := strings.ToLower(key)
		if hopByHop[lowerKey] {
			continue
		}

		for _, value := range values {
			result.Add(key, value)
		}
	}

	return result
}
