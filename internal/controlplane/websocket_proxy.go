package controlplane

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
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

	m.logger.WithFields(logrus.Fields{
		"stream_id": streamID,
		"method":    method,
		"path":      path,
		"protocol":  protocol,
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

	k8sURL := fmt.Sprintf("https://kubernetes.default.svc%s", path)
	if query != "" {
		k8sURL = fmt.Sprintf("%s?%s", k8sURL, query)
	}

	stream.logger.WithField("url", k8sURL).Debug("Connecting to Kubernetes API")

	dialer := websocket.Dialer{
		TLSClientConfig: m.client.tlsConfig,
		Subprotocols:    []string{protocol},
	}

	conn, resp, err := dialer.Dial(k8sURL, headers)
	if err != nil {
		errMsg := fmt.Sprintf("failed to connect to K8s API: %v", err)
		if resp != nil {
			errMsg = fmt.Sprintf("%s (status: %d)", errMsg, resp.StatusCode)
		}
		stream.logger.WithError(err).Error("Failed to connect to Kubernetes API")
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
