package controlplane

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	wsutil "github.com/pipeops/pipeops-vm-agent/internal/websocket"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/rest"
)

// WebSocketStream represents an active WebSocket proxy stream
type WebSocketStream struct {
	streamID  string
	conn      *websocket.Conn
	dataCh    chan []byte
	closeCh   chan struct{}
	ctx       context.Context
	cancel    context.CancelFunc
	logger    *logrus.Entry
	startTime time.Time
}

// WebSocketProxyManager manages active WebSocket streams
type WebSocketProxyManager struct {
	streams   map[string]*WebSocketStream
	streamsMu sync.RWMutex
	logger    *logrus.Logger
	client    *WebSocketClient
}

// NewWebSocketProxyManager creates a new WebSocket proxy manager
func NewWebSocketProxyManager(client *WebSocketClient, logger *logrus.Logger) *WebSocketProxyManager {
	return &WebSocketProxyManager{
		streams: make(map[string]*WebSocketStream),
		logger:  logger,
		client:  client,
	}
}

func (m *WebSocketProxyManager) HasStream(streamID string) bool {
	if streamID == "" {
		return false
	}

	m.streamsMu.RLock()
	_, ok := m.streams[streamID]
	m.streamsMu.RUnlock()
	return ok
}

// HandleWebSocketProxyStart handles the proxy_websocket_start message
func (m *WebSocketProxyManager) HandleWebSocketProxyStart(msg *WebSocketMessage) {
	payload := msg.Payload
	streamID, ok := payload["stream_id"].(string)
	if !ok || streamID == "" {
		m.logger.Error("Missing or invalid stream_id in proxy_websocket_start")
		m.sendWebSocketError(msg.RequestID, "missing stream_id")
		return
	}

	method, _ := payload["method"].(string)
	path, _ := payload["path"].(string)
	query, _ := payload["query"].(string)
	protocol, _ := payload["protocol"].(string)

	// Validate path to prevent SSRF attacks
	// Path must start with /api or /apis (Kubernetes API endpoints only)
	if !isValidK8sAPIPath(path) {
		m.logger.WithFields(logrus.Fields{
			"stream_id": streamID,
			"path":      path,
		}).Warn("Rejected WebSocket proxy request with invalid path")
		m.sendWebSocketError(msg.RequestID, "invalid path: must be a Kubernetes API path")
		return
	}

	headers := make(map[string][]string)
	if headersPayload, ok := payload["headers"].(map[string]interface{}); ok {
		for key, value := range headersPayload {
			switch v := value.(type) {
			case []interface{}:
				strSlice := make([]string, 0, len(v))
				for _, item := range v {
					if str, ok := item.(string); ok {
						strSlice = append(strSlice, str)
					}
				}
				headers[key] = strSlice
			case string:
				headers[key] = []string{v}
			}
		}
	}

	// Log header keys (not values) for debugging auth flow
	headerKeys := make([]string, 0, len(headers))
	for k := range headers {
		headerKeys = append(headerKeys, k)
	}

	m.logger.WithFields(logrus.Fields{
		"stream_id":   streamID,
		"method":      method,
		"path":        path,
		"protocol":    protocol,
		"header_keys": headerKeys,
	}).Info("WebSocket proxy start requested")

	ctx, cancel := context.WithCancel(context.Background())
	stream := &WebSocketStream{
		streamID:  streamID,
		dataCh:    make(chan []byte, 100),
		closeCh:   make(chan struct{}),
		ctx:       ctx,
		cancel:    cancel,
		logger:    m.logger.WithField("stream_id", streamID),
		startTime: time.Now(),
	}

	m.streamsMu.Lock()
	m.streams[streamID] = stream
	m.streamsMu.Unlock()

	go m.connectToKubernetes(stream, method, path, query, headers, protocol)
}

// HandleWebSocketProxyData handles the proxy_websocket_data message
func (m *WebSocketProxyManager) HandleWebSocketProxyData(msg *WebSocketMessage) {
	payload := msg.Payload
	streamID, ok := payload["stream_id"].(string)
	if !ok || streamID == "" {
		m.logger.Error("Missing or invalid stream_id in proxy_websocket_data")
		return
	}

	dataStr, ok := payload["data"].(string)
	if !ok {
		m.logger.WithField("stream_id", streamID).Error("Missing or invalid data in proxy_websocket_data")
		return
	}

	data, err := base64.StdEncoding.DecodeString(dataStr)
	if err != nil {
		m.logger.WithError(err).WithField("stream_id", streamID).Error("Failed to decode base64 data")
		return
	}

	m.streamsMu.RLock()
	stream, ok := m.streams[streamID]
	m.streamsMu.RUnlock()

	if !ok {
		m.logger.WithField("stream_id", streamID).Warn("Received data for unknown stream")
		return
	}

	select {
	case stream.dataCh <- data:
	case <-stream.ctx.Done():
	default:
		m.logger.WithField("stream_id", streamID).Warn("Data channel full, dropping message")
	}
}

// HandleWebSocketProxyClose handles the proxy_websocket_close message
func (m *WebSocketProxyManager) HandleWebSocketProxyClose(msg *WebSocketMessage) {
	payload := msg.Payload
	streamID, ok := payload["stream_id"].(string)
	if !ok || streamID == "" {
		m.logger.Error("Missing or invalid stream_id in proxy_websocket_close")
		return
	}

	m.logger.WithField("stream_id", streamID).Info("WebSocket proxy close requested")
	m.closeStream(streamID)
}

// connectToKubernetes establishes a WebSocket connection to the Kubernetes API
func (m *WebSocketProxyManager) connectToKubernetes(stream *WebSocketStream, method, path, query string, headers map[string][]string, protocol string) {
	defer m.closeStream(stream.streamID)

	// Determine Kubernetes API host/TLS/auth from in-cluster config when available.
	host := m.client.getK8sAPIHost()
	tlsConfig := m.client.tlsConfig
	gatewayInjectedAuth := false
	if authVals, hasAuth := headers["Authorization"]; hasAuth && len(authVals) > 0 && authVals[0] != "" {
		gatewayInjectedAuth = true
	}
	if cfg, err := rest.InClusterConfig(); err == nil {
		if u, parseErr := url.Parse(cfg.Host); parseErr == nil && u.Host != "" {
			host = u.Host
		}
		if k8sTLS, tlsErr := rest.TLSConfigFor(cfg); tlsErr == nil && k8sTLS != nil {
			tlsConfig = k8sTLS
		}
		// Only use in-cluster ServiceAccount token as a fallback when the
		// gateway didn't inject an Authorization header. The gateway's
		// injectCredentials sets the cluster-specific bearer token which
		// must take precedence — matching the HTTP proxy behaviour in
		// agent.go ("DO NOT override Authorization header").
		if cfg.BearerToken != "" {
			if headers == nil {
				headers = make(map[string][]string)
			}
			if _, hasAuth := headers["Authorization"]; !hasAuth {
				headers["Authorization"] = []string{fmt.Sprintf("Bearer %s", strings.TrimSpace(cfg.BearerToken))}
				stream.logger.WithField("token_preview", truncateToken(cfg.BearerToken)).
					Warn("No Authorization from gateway; falling back to in-cluster SA token")
			}
		}
	}
	// Log which auth source is being used
	authSource := "gateway-injected"
	if !gatewayInjectedAuth {
		authSource = "in-cluster-sa-fallback"
	}
	if authVals, ok := headers["Authorization"]; ok && len(authVals) > 0 {
		stream.logger.WithFields(logrus.Fields{
			"auth_source":   authSource,
			"token_preview": truncateToken(authVals[0]),
		}).Info("K8s WebSocket auth resolved")
	} else {
		stream.logger.Warn("No Authorization header set for K8s WebSocket dial — expect 401/403")
	}

	// Sanitize path and query to prevent injection attacks
	// Use url.Parse to properly construct the URL
	sanitizedPath := sanitizeURLPath(path)
	sanitizedQuery := sanitizeQueryString(query)

	// Gorilla websocket dialer requires ws/wss schemes. The Kubernetes API is served over HTTPS in-cluster,
	// so use wss:// to perform a secure WebSocket upgrade against the apiserver service.
	k8sURL := fmt.Sprintf("wss://%s%s", host, sanitizedPath)
	if sanitizedQuery != "" {
		k8sURL = fmt.Sprintf("%s?%s", k8sURL, sanitizedQuery)
	}

	// Final validation: parse the URL to ensure it's well-formed
	parsedURL, err := url.Parse(k8sURL)
	if err != nil {
		stream.logger.WithError(err).Error("Invalid URL constructed for Kubernetes API")
		m.sendWebSocketError(stream.streamID, "invalid URL")
		return
	}

	// Ensure the host hasn't been manipulated
	if parsedURL.Host != host {
		stream.logger.WithFields(logrus.Fields{
			"expected_host": host,
			"actual_host":   parsedURL.Host,
		}).Error("URL host manipulation detected")
		m.sendWebSocketError(stream.streamID, "invalid URL: host mismatch")
		return
	}

	safeURL := parsedURL.String()
	stream.logger.WithField("url", safeURL).Debug("Connecting to Kubernetes API")

	preparedHeaders := wsutil.PrepareHeaders(headers)
	// Ensure we don't pass Sec-WebSocket* headers that the dialer sets automatically.
	preparedHeaders.Del("Sec-WebSocket-Key")
	preparedHeaders.Del("Sec-WebSocket-Version")
	preparedHeaders.Del("Sec-WebSocket-Extensions")

	dialer := websocket.Dialer{
		TLSClientConfig: tlsConfig,
	}
	subprotocols := extractWebSocketSubprotocols(headers, protocol)
	if len(subprotocols) == 0 && isK8sStreamingPath(sanitizedPath) {
		// Kubernetes exec/attach/portforward endpoints require a subprotocol negotiation.
		// If none was forwarded from the client/controller, inject the standard K8s SPDY
		// subprotocols so the API server accepts the WebSocket upgrade.
		subprotocols = []string{
			"v4.channel.k8s.io",
			"v3.channel.k8s.io",
			"v2.channel.k8s.io",
			"channel.k8s.io",
		}
		stream.logger.Debug("No subprotocol provided; injecting default K8s streaming subprotocols")
	}
	if len(subprotocols) > 0 {
		dialer.Subprotocols = subprotocols
		preparedHeaders.Del("Sec-WebSocket-Protocol")
	}

	conn, resp, err := dialer.Dial(k8sURL, preparedHeaders)
	if err != nil {
		errMsg := fmt.Sprintf("failed to connect to K8s API: %v", err)
		if resp != nil {
			errMsg = fmt.Sprintf("%s (status: %d)", errMsg, resp.StatusCode)
			// Read response body for detailed K8s error message (e.g., RBAC denial reason)
			if resp.Body != nil {
				defer resp.Body.Close()
				if body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024)); readErr == nil && len(body) > 0 {
					stream.logger.WithField("response_body", string(body)).Error("K8s API rejection details")
				}
			}
		}
		stream.logger.WithField("detail", errMsg).WithError(err).Error("Failed to connect to Kubernetes API")
		m.sendWebSocketError(stream.streamID, errMsg)
		return
	}
	defer conn.Close()

	stream.conn = conn
	stream.logger.Info("Connected to Kubernetes API")

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		m.relayClientToK8s(stream)
	}()

	go func() {
		defer wg.Done()
		m.relayK8sToClient(stream)
	}()

	wg.Wait()

	duration := time.Since(stream.startTime)
	stream.logger.WithField("duration", duration).Info("WebSocket stream closed")
}

// relayClientToK8s relays data from the client (via controller) to Kubernetes
func (m *WebSocketProxyManager) relayClientToK8s(stream *WebSocketStream) {
	for {
		select {
		case <-stream.ctx.Done():
			return
		case <-stream.closeCh:
			return
		case data := <-stream.dataCh:
			if len(data) == 0 {
				continue
			}

			messageType := int(data[0])
			messageData := data[1:]

			if err := stream.conn.WriteMessage(messageType, messageData); err != nil {
				stream.logger.WithError(err).Error("Failed to write to Kubernetes WebSocket")
				stream.cancel()
				return
			}
		}
	}
}

// relayK8sToClient relays data from Kubernetes to the client (via controller)
func (m *WebSocketProxyManager) relayK8sToClient(stream *WebSocketStream) {
	for {
		messageType, data, err := stream.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				stream.logger.WithError(err).Warn("Kubernetes WebSocket closed unexpectedly")
			} else {
				stream.logger.Debug("Kubernetes WebSocket closed normally")
			}
			stream.cancel()
			return
		}

		fullData := make([]byte, len(data)+1)
		fullData[0] = byte(messageType)
		copy(fullData[1:], data)

		if err := m.sendWebSocketDataToController(stream.streamID, fullData); err != nil {
			stream.logger.WithError(err).Error("Failed to send data to controller")
			stream.cancel()
			return
		}
	}
}

// sendWebSocketDataToController sends WebSocket data to the controller
func (m *WebSocketProxyManager) sendWebSocketDataToController(streamID string, data []byte) error {
	encoded := base64.StdEncoding.EncodeToString(data)

	msg := &WebSocketMessage{
		Type:      "proxy_websocket_data",
		RequestID: streamID,
		Timestamp: time.Now(),
		Payload: map[string]interface{}{
			"stream_id": streamID,
			"data":      encoded,
		},
	}

	return m.client.sendMessage(msg)
}

// sendWebSocketClose sends a WebSocket close message to the controller
func (m *WebSocketProxyManager) sendWebSocketClose(streamID string) error {
	msg := &WebSocketMessage{
		Type:      "proxy_websocket_close",
		RequestID: streamID,
		Timestamp: time.Now(),
		Payload: map[string]interface{}{
			"stream_id": streamID,
		},
	}

	return m.client.sendMessage(msg)
}

// sendWebSocketError sends a WebSocket error message to the controller
func (m *WebSocketProxyManager) sendWebSocketError(streamID, errorMsg string) error {
	msg := &WebSocketMessage{
		Type:      "proxy_error",
		RequestID: streamID,
		Timestamp: time.Now(),
		Payload: map[string]interface{}{
			"request_id": streamID,
			"error":      errorMsg,
		},
	}

	return m.client.sendMessage(msg)
}

// closeStream closes a WebSocket stream and cleans up resources
func (m *WebSocketProxyManager) closeStream(streamID string) {
	m.streamsMu.Lock()
	stream, ok := m.streams[streamID]
	if !ok {
		m.streamsMu.Unlock()
		return
	}
	delete(m.streams, streamID)
	m.streamsMu.Unlock()

	stream.cancel()
	close(stream.closeCh)

	if stream.conn != nil {
		stream.conn.Close()
	}

	m.sendWebSocketClose(streamID)
	stream.logger.Info("Stream closed and cleaned up")
}

// CloseAll closes all active streams
func (m *WebSocketProxyManager) CloseAll() {
	m.streamsMu.Lock()
	streamIDs := make([]string, 0, len(m.streams))
	for id := range m.streams {
		streamIDs = append(streamIDs, id)
	}
	m.streamsMu.Unlock()

	for _, id := range streamIDs {
		m.closeStream(id)
	}
}

// isValidK8sAPIPath validates that the path is a legitimate Kubernetes API path
// to prevent SSRF attacks where malicious paths could redirect to other hosts
func isValidK8sAPIPath(path string) bool {
	// Path must not be empty
	if path == "" {
		return false
	}

	// Normalize path - remove any path traversal attempts
	cleanPath := strings.ReplaceAll(path, "..", "")
	cleanPath = strings.ReplaceAll(cleanPath, "//", "/")

	// Path must start with a forward slash
	if !strings.HasPrefix(cleanPath, "/") {
		return false
	}

	// Check for URL encoding that might bypass validation
	// Reject paths with suspicious characters
	if strings.Contains(path, "%") || strings.Contains(path, "\\") {
		return false
	}

	// Check for newlines/carriage returns (HTTP header injection)
	if strings.ContainsAny(path, "\r\n") {
		return false
	}

	// Allowed Kubernetes API path prefixes
	allowedPrefixes := []string{
		"/api/",     // Core API (pods, services, etc.)
		"/apis/",    // Extended APIs (deployments, etc.)
		"/version",  // Cluster version
		"/healthz",  // Health check
		"/readyz",   // Readiness check
		"/livez",    // Liveness check
		"/openapi/", // OpenAPI spec
		"/api",      // Exact match for /api
		"/apis",     // Exact match for /apis
	}

	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(cleanPath, prefix) {
			return true
		}
	}

	return false
}

// sanitizeURLPath removes dangerous characters from URL paths
func sanitizeURLPath(path string) string {
	// Remove newlines and carriage returns
	path = strings.ReplaceAll(path, "\r", "")
	path = strings.ReplaceAll(path, "\n", "")

	// Remove null bytes
	path = strings.ReplaceAll(path, "\x00", "")

	// Remove path traversal sequences
	path = strings.ReplaceAll(path, "..", "")

	// Normalize multiple slashes
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}

	// Ensure path starts with /
	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	return path
}

// sanitizeQueryString removes dangerous characters from query strings
func sanitizeQueryString(query string) string {
	// Remove newlines and carriage returns
	query = strings.ReplaceAll(query, "\r", "")
	query = strings.ReplaceAll(query, "\n", "")

	// Remove null bytes
	query = strings.ReplaceAll(query, "\x00", "")

	return query
}

// isK8sStreamingPath returns true if the path targets a Kubernetes streaming
// endpoint (exec, attach, or portforward) that requires WebSocket subprotocol
// negotiation.
func isK8sStreamingPath(path string) bool {
	segments := strings.Split(path, "/")
	for _, seg := range segments {
		switch seg {
		case "exec", "attach", "portforward":
			return true
		}
	}
	return false
}

func extractWebSocketSubprotocols(headers map[string][]string, explicitProtocol string) []string {
	protocols := make([]string, 0, 4)
	seen := make(map[string]struct{})

	addProtocol := func(raw string) {
		for _, part := range strings.Split(raw, ",") {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			if _, exists := seen[p]; exists {
				continue
			}
			seen[p] = struct{}{}
			protocols = append(protocols, p)
		}
	}

	addProtocol(explicitProtocol)
	for key, values := range headers {
		if !strings.EqualFold(key, "Sec-WebSocket-Protocol") {
			continue
		}
		for _, value := range values {
			addProtocol(value)
		}
	}

	return protocols
}

// truncateToken returns a safe preview of a bearer token for logging.
// Shows the first 10 and last 6 characters with the middle redacted.
func truncateToken(token string) string {
	// Strip "Bearer " prefix if present
	t := strings.TrimPrefix(token, "Bearer ")
	if len(t) <= 20 {
		return "[REDACTED-SHORT]"
	}
	return t[:10] + "..." + t[len(t)-6:]
}
