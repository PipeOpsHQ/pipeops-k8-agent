package controlplane

import (
	"encoding/base64"
	"testing"

	"github.com/pipeops/pipeops-vm-agent/pkg/types"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseProxyRequest_WebSocketFlagsAndHeadData(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client, err := NewWebSocketClient("ws://example.com", "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	head := []byte{0x01, 0x02, 0x03}
	msg := &WebSocketMessage{
		Type:      "proxy_request",
		RequestID: "req-123",
		Payload: map[string]interface{}{
			"method":        "GET",
			"path":          "/ws",
			"is_websocket":  true,
			"use_zero_copy": true,
			"head_data":     base64.StdEncoding.EncodeToString(head),
			"protocol":      "v4.channel.k8s.io",
			"headers": map[string]interface{}{
				"Host": []interface{}{"example.local"},
			},
		},
	}

	req, err := client.parseProxyRequest(msg)
	require.NoError(t, err)
	assert.True(t, req.IsWebSocket)
	assert.True(t, req.UseZeroCopy)
	assert.Equal(t, head, req.HeadData)
	assert.Equal(t, []string{"v4.channel.k8s.io"}, req.Headers["Sec-WebSocket-Protocol"])
}

func TestParseProxyRequest_CamelCaseWebSocketFields(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client, err := NewWebSocketClient("ws://example.com", "test-token", "agent-123", types.DefaultTimeouts(), nil, logger)
	require.NoError(t, err)

	msg := &WebSocketMessage{
		Type:      "proxy_request",
		RequestID: "req-234",
		Payload: map[string]interface{}{
			"method":      "GET",
			"path":        "/socket",
			"isWebSocket": true,
			"useZeroCopy": true,
			"headData":    "plain-text-head",
		},
	}

	req, err := client.parseProxyRequest(msg)
	require.NoError(t, err)
	assert.True(t, req.IsWebSocket)
	assert.True(t, req.UseZeroCopy)
	assert.Equal(t, []byte("plain-text-head"), req.HeadData)
}
