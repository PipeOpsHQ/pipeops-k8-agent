package server

import (
	"encoding/json"
	"net/http"
	"runtime"
	"time"
	"strings"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // In production, implement proper origin checking
	},
	EnableCompression: false,
}

// WebSocketMessage represents a WebSocket message
type WebSocketMessage struct {
	Type      string      `json:"type"`
	Timestamp time.Time   `json:"timestamp"`
	Data      interface{} `json:"data"`
}

// handleWebSocket handles WebSocket connections for real-time communication
func (s *Server) handleWebSocket(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		s.logger.WithError(err).Error("Failed to upgrade WebSocket connection")
		return
	}
	defer conn.Close()

	s.logger.Info("WebSocket connection established")

	// Send initial status
	s.sendWebSocketMessage(conn, "status", gin.H{
		"agent_id": s.config.Agent.ID,
		"status":   "connected",
		"features": s.features,
	})

	// Handle messages
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			var message WebSocketMessage
			err := conn.ReadJSON(&message)
			if err != nil {
				s.logger.WithError(err).Debug("WebSocket connection closed")
				return
			}

			s.handleWebSocketMessage(conn, &message)
		}
	}
}

// handleEventStream provides Server-Sent Events for real-time updates
func (s *Server) handleEventStream(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Streaming unsupported"})
		return
	}

	// Send initial event
	s.sendSSEEvent(c.Writer, "connected", gin.H{
		"agent_id":  s.config.Agent.ID,
		"timestamp": time.Now(),
	})
	flusher.Flush()

	// Send periodic updates
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
			// Send health update
			data := gin.H{
				"type":      "health_update",
				"timestamp": time.Now(),
				"agent": gin.H{
					"healthy": true,
					"uptime":  time.Since(s.startTime).String(),
				},
				"tunnel": gin.H{
					"enabled": true,
					"type":    "portainer-style",
				},
			}

			s.sendSSEEvent(c.Writer, "health_update", data)
			flusher.Flush()
		}
	}
}

// handleLogStream provides real-time log streaming
func (s *Server) handleLogStream(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")

	// In a real implementation, this would connect to log streaming
	// For now, send periodic log messages
	c.SSEvent("log", gin.H{
		"level":     "info",
		"message":   "Log streaming started",
		"timestamp": time.Now(),
	})

	c.Writer.Flush()
}

// sendWebSocketMessage sends a message via WebSocket
func (s *Server) sendWebSocketMessage(conn *websocket.Conn, msgType string, data interface{}) error {
	message := WebSocketMessage{
		Type:      msgType,
		Timestamp: time.Now(),
		Data:      data,
	}

	return conn.WriteJSON(message)
}

// sendSSEEvent sends a Server-Sent Event
func (s *Server) sendSSEEvent(w gin.ResponseWriter, event string, data interface{}) {
	jsonData, _ := json.Marshal(data)
	w.Write([]byte("event: " + event + "\n"))
	w.Write([]byte("data: " + string(jsonData) + "\n\n"))
}

// sanitizeLogString removes line breaks from a string to prevent log forging
func sanitizeLogString(s string) string {
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

// handleWebSocketMessage handles incoming WebSocket messages
func (s *Server) handleWebSocketMessage(conn *websocket.Conn, message *WebSocketMessage) {
	safeType := sanitizeLogString(message.Type)

	var safeData interface{}
	switch v := message.Data.(type) {
	case string:
		safeData = sanitizeLogString(v)
	default:
		// marshal to JSON and sanitize string output
		js, err := json.Marshal(v)
		if err != nil {
			safeData = "[unloggable data]"
		} else {
			safeData = sanitizeLogString(string(js))
		}
	}

	s.logger.WithFields(logrus.Fields{
		"type": safeType,
		"data": safeData,
	}).Debug("Received WebSocket message")
	switch message.Type {
	case "ping":
		s.sendWebSocketMessage(conn, "pong", gin.H{
			"timestamp": time.Now(),
		})

	case "get_status":
		response := gin.H{
			"agent_id": s.config.Agent.ID,
			"uptime":   time.Since(s.startTime).String(),
			"tunnel": gin.H{
				"enabled": true,
				"type":    "portainer-style",
			},
			"features": s.features,
		}

		s.sendWebSocketMessage(conn, "status_response", response)

	case "get_metrics":
		// Send real-time metrics
		s.sendWebSocketMessage(conn, "metrics_response", s.getRuntimeMetrics())

	default:
		s.sendWebSocketMessage(conn, "error", gin.H{
			"message": "Unknown message type: " + message.Type,
		})
	}
}

// getRuntimeMetrics returns current runtime metrics
func (s *Server) getRuntimeMetrics() gin.H {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	return gin.H{
		"memory": gin.H{
			"alloc_mb": memStats.Alloc / 1024 / 1024,
			"sys_mb":   memStats.Sys / 1024 / 1024,
		},
		"goroutines": runtime.NumGoroutine(),
		"uptime":     time.Since(s.startTime).String(),
		"timestamp":  time.Now(),
	}
}
