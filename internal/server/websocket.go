package server

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // In production, implement proper origin checking
	},
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
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			clusterStatus, err := s.k8sClient.GetClusterStatus(ctx)
			cancel()

			data := gin.H{
				"type":           "health_update",
				"timestamp":      time.Now(),
				"cluster_status": clusterStatus,
				"uptime":         time.Since(s.startTime).String(),
			}

			if err != nil {
				data["error"] = err.Error()
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

// handleWebSocketMessage handles incoming WebSocket messages
func (s *Server) handleWebSocketMessage(conn *websocket.Conn, message *WebSocketMessage) {
	s.logger.WithFields(logrus.Fields{
		"type": message.Type,
		"data": message.Data,
	}).Debug("Received WebSocket message")

	switch message.Type {
	case "ping":
		s.sendWebSocketMessage(conn, "pong", gin.H{
			"timestamp": time.Now(),
		})

	case "get_status":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		clusterStatus, err := s.k8sClient.GetClusterStatus(ctx)
		response := gin.H{
			"agent_id":       s.config.Agent.ID,
			"uptime":         time.Since(s.startTime).String(),
			"cluster_status": clusterStatus,
			"features":       s.features,
		}

		if err != nil {
			response["error"] = err.Error()
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
