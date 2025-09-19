package frp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Logger interface for structured logging
type Logger interface {
	Info(msg string, args ...interface{})
	Error(msg string, args ...interface{})
	Warn(msg string, args ...interface{})
	Debug(msg string, args ...interface{})
}

// DefaultLogger implements Logger using standard log package
type DefaultLogger struct{}

func (l *DefaultLogger) Info(msg string, args ...interface{})  { log.Printf("INFO: "+msg, args...) }
func (l *DefaultLogger) Error(msg string, args ...interface{}) { log.Printf("ERROR: "+msg, args...) }
func (l *DefaultLogger) Warn(msg string, args ...interface{})  { log.Printf("WARN: "+msg, args...) }
func (l *DefaultLogger) Debug(msg string, args ...interface{}) { log.Printf("DEBUG: "+msg, args...) }

// AuthService handles FRP token authentication and authorization
type AuthService struct {
	validTokens  map[string]*AgentInfo
	tokensMux    sync.RWMutex
	jwtSecret    []byte
	controlPlane *ControlPlaneClient
	log          Logger
}

// AgentInfo contains information about an authenticated agent
type AgentInfo struct {
	ClusterID    string    `json:"cluster_id"`
	ClusterName  string    `json:"cluster_name"`
	Organization string    `json:"organization"`
	Permissions  []string  `json:"permissions"`
	ExpiresAt    time.Time `json:"expires_at"`
	LastSeen     time.Time `json:"last_seen"`
	IPWhitelist  []string  `json:"ip_whitelist,omitempty"`
}

// TokenRequest represents a request for an FRP token
type TokenRequest struct {
	AgentToken  string `json:"agent_token"`
	ClusterID   string `json:"cluster_id"`
	ClusterName string `json:"cluster_name"`
}

// TokenResponse represents the response containing an FRP token
type TokenResponse struct {
	FRPToken    string    `json:"frp_token"`
	ExpiresAt   time.Time `json:"expires_at"`
	Permissions []string  `json:"permissions"`
}

// ControlPlaneClient handles communication with the PipeOps control plane
type ControlPlaneClient struct {
	baseURL string
	client  *http.Client
	log     Logger
}

// NewAuthService creates a new authentication service
func NewAuthService(jwtSecret []byte, controlPlaneURL string, log Logger) *AuthService {
	return &AuthService{
		validTokens:  make(map[string]*AgentInfo),
		jwtSecret:    jwtSecret,
		controlPlane: NewControlPlaneClient(controlPlaneURL, log),
		log:          log,
	}
}

// NewControlPlaneClient creates a new control plane client
func NewControlPlaneClient(baseURL string, log Logger) *ControlPlaneClient {
	return &ControlPlaneClient{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		log: log,
	}
}

// GetFRPToken handles HTTP requests for FRP tokens
func (a *AuthService) GetFRPToken(w http.ResponseWriter, r *http.Request) {
	var req TokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.log.Error("Invalid token request", "error", err)
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	// Validate agent token with control plane
	agentInfo, err := a.controlPlane.ValidateAgent(req.AgentToken, req.ClusterID)
	if err != nil {
		a.log.Error("Agent validation failed", "cluster_id", req.ClusterID, "error", err)
		http.Error(w, "invalid agent token", http.StatusUnauthorized)
		return
	}

	// Check if cluster is authorized
	if !a.isClusterAuthorized(agentInfo) {
		a.log.Warn("Cluster not authorized", "cluster_id", req.ClusterID, "organization", agentInfo.Organization)
		http.Error(w, "cluster not authorized", http.StatusForbidden)
		return
	}

	// Generate FRP token
	frpToken, expiresAt, err := a.generateFRPToken(agentInfo)
	if err != nil {
		a.log.Error("Failed to generate FRP token", "cluster_id", req.ClusterID, "error", err)
		http.Error(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

	// Store token info
	a.tokensMux.Lock()
	a.validTokens[frpToken] = &AgentInfo{
		ClusterID:    agentInfo.ClusterID,
		ClusterName:  agentInfo.ClusterName,
		Organization: agentInfo.Organization,
		Permissions:  agentInfo.Permissions,
		ExpiresAt:    expiresAt,
		LastSeen:     time.Now(),
		IPWhitelist:  agentInfo.IPWhitelist,
	}
	a.tokensMux.Unlock()

	a.log.Info("FRP token generated successfully",
		"cluster_id", req.ClusterID,
		"expires_at", expiresAt,
		"permissions", agentInfo.Permissions,
	)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(TokenResponse{
		FRPToken:    frpToken,
		ExpiresAt:   expiresAt,
		Permissions: agentInfo.Permissions,
	})
}

// ValidateFRPToken validates an FRP token (called by FRP server)
func (a *AuthService) ValidateFRPToken(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Frp-Token")
	clientIP := r.Header.Get("X-Forwarded-For")
	if clientIP == "" {
		clientIP = r.RemoteAddr
	}

	agentInfo, err := a.validateToken(token, clientIP)
	if err != nil {
		a.log.Warn("FRP token validation failed", "error", err, "client_ip", clientIP)
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	// Return agent information for FRP server
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"valid":        true,
		"cluster_id":   agentInfo.ClusterID,
		"cluster_name": agentInfo.ClusterName,
		"permissions":  agentInfo.Permissions,
	})
}

// validateToken validates a token and returns agent info
func (a *AuthService) validateToken(token, clientIP string) (*AgentInfo, error) {
	a.tokensMux.RLock()
	agentInfo, exists := a.validTokens[token]
	a.tokensMux.RUnlock()

	if !exists {
		return nil, fmt.Errorf("invalid token")
	}

	if time.Now().After(agentInfo.ExpiresAt) {
		a.tokensMux.Lock()
		delete(a.validTokens, token)
		a.tokensMux.Unlock()
		return nil, fmt.Errorf("token expired")
	}

	// Check IP whitelist if configured
	if len(agentInfo.IPWhitelist) > 0 {
		allowed := false
		for _, allowedIP := range agentInfo.IPWhitelist {
			if clientIP == allowedIP {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("IP not whitelisted: %s", clientIP)
		}
	}

	// Update last seen
	a.tokensMux.Lock()
	agentInfo.LastSeen = time.Now()
	a.tokensMux.Unlock()

	return agentInfo, nil
}

// generateFRPToken generates a secure token for FRP authentication
func (a *AuthService) generateFRPToken(agentInfo *AgentInfo) (string, time.Time, error) {
	expiresAt := time.Now().Add(24 * time.Hour) // 24 hour tokens

	// Create token payload
	payload := map[string]interface{}{
		"cluster_id":   agentInfo.ClusterID,
		"cluster_name": agentInfo.ClusterName,
		"organization": agentInfo.Organization,
		"permissions":  agentInfo.Permissions,
		"exp":          expiresAt.Unix(),
		"iat":          time.Now().Unix(),
		"iss":          "pipeops-frp-auth",
	}

	// Marshal payload to JSON
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Encode payload as base64
	encodedPayload := base64.URLEncoding.EncodeToString(payloadBytes)

	// Generate HMAC signature
	h := hmac.New(sha256.New, a.jwtSecret)
	h.Write([]byte(encodedPayload))
	signature := base64.URLEncoding.EncodeToString(h.Sum(nil))

	// Combine payload and signature
	token := encodedPayload + "." + signature

	return token, expiresAt, nil
}

// verifyFRPToken verifies the signature of an FRP token
func (a *AuthService) verifyFRPToken(token string) (map[string]interface{}, error) {
	// Split token into payload and signature
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid token format")
	}

	encodedPayload, encodedSignature := parts[0], parts[1]

	// Verify signature
	h := hmac.New(sha256.New, a.jwtSecret)
	h.Write([]byte(encodedPayload))
	expectedSignature := base64.URLEncoding.EncodeToString(h.Sum(nil))

	if !hmac.Equal([]byte(encodedSignature), []byte(expectedSignature)) {
		return nil, fmt.Errorf("invalid token signature")
	}

	// Decode payload
	payloadBytes, err := base64.URLEncoding.DecodeString(encodedPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to decode payload: %w", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	// Check expiration
	if exp, ok := payload["exp"].(float64); ok {
		if time.Now().Unix() > int64(exp) {
			return nil, fmt.Errorf("token expired")
		}
	}

	return payload, nil
}

// isClusterAuthorized checks if a cluster is authorized to use FRP
func (a *AuthService) isClusterAuthorized(agentInfo *AgentInfo) bool {
	// Implement your authorization logic here
	// For example, check organization limits, cluster permissions, etc.

	// Basic checks
	if agentInfo.ClusterID == "" || agentInfo.Organization == "" {
		return false
	}

	// Check if organization has active subscription
	// Check if cluster is within resource limits
	// etc.

	return true // Simplified for example
}

// StartCleanupRoutine starts a routine to clean up expired tokens
func (a *AuthService) StartCleanupRoutine(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	a.log.Info("Starting token cleanup routine")

	for {
		select {
		case <-ctx.Done():
			a.log.Info("Stopping token cleanup routine")
			return
		case <-ticker.C:
			a.cleanupExpiredTokens()
		}
	}
}

// cleanupExpiredTokens removes expired tokens from memory
func (a *AuthService) cleanupExpiredTokens() {
	a.tokensMux.Lock()
	defer a.tokensMux.Unlock()

	now := time.Now()
	expiredCount := 0

	for token, info := range a.validTokens {
		if now.After(info.ExpiresAt) {
			delete(a.validTokens, token)
			expiredCount++
		}
	}

	if expiredCount > 0 {
		a.log.Info("Cleaned up expired tokens", "count", expiredCount, "remaining", len(a.validTokens))
	}
}

// GetTokenStats returns statistics about active tokens
func (a *AuthService) GetTokenStats() map[string]interface{} {
	a.tokensMux.RLock()
	defer a.tokensMux.RUnlock()

	stats := map[string]interface{}{
		"total_tokens":    len(a.validTokens),
		"by_organization": make(map[string]int),
		"oldest_token":    time.Now(),
		"newest_token":    time.Time{},
	}

	orgCounts := make(map[string]int)
	var oldest, newest time.Time

	for _, info := range a.validTokens {
		orgCounts[info.Organization]++

		if oldest.IsZero() || info.LastSeen.Before(oldest) {
			oldest = info.LastSeen
		}
		if newest.IsZero() || info.LastSeen.After(newest) {
			newest = info.LastSeen
		}
	}

	stats["by_organization"] = orgCounts
	if !oldest.IsZero() {
		stats["oldest_token"] = oldest
	}
	if !newest.IsZero() {
		stats["newest_token"] = newest
	}

	return stats
}

// ValidateAgent validates an agent token with the control plane
func (c *ControlPlaneClient) ValidateAgent(agentToken, clusterID string) (*AgentInfo, error) {
	reqBody := map[string]string{
		"agent_token": agentToken,
		"cluster_id":  clusterID,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/agents/validate", c.baseURL)
	resp, err := c.client.Post(url, "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("validation request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("validation failed with status: %d", resp.StatusCode)
	}

	var agentInfo AgentInfo
	if err := json.NewDecoder(resp.Body).Decode(&agentInfo); err != nil {
		return nil, fmt.Errorf("failed to decode validation response: %w", err)
	}

	c.log.Debug("Agent validated successfully",
		"cluster_id", clusterID,
		"organization", agentInfo.Organization,
		"permissions", agentInfo.Permissions,
	)

	return &agentInfo, nil
}
