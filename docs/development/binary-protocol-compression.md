# Binary Protocol and Compression Implementation Guide

## Overview

This document describes the implementation of binary protocol and compression support in the PipeOps agent to eliminate base64 overhead and reduce bandwidth usage.

## Status

**NOT YET IMPLEMENTED** - This document serves as an implementation guide.

## Background

Currently, all request/response bodies are base64-encoded, adding 33% overhead. Additionally, no compression is applied to text-based responses, wasting bandwidth.

### Performance Impact

- **Base64 overhead**: 33% increase in payload size
- **No compression**: Missing 40-60% potential bandwidth savings for JSON/text
- **Combined impact**: 50-70% bandwidth waste

### Target Performance

After implementation:
- **Bandwidth**: -50-70% (binary + compression)
- **Latency**: -20-30% (less data transfer)
- **Infrastructure costs**: -50% (bandwidth)

## Part 1: Binary Protocol Support

### Objective

Eliminate base64 encoding overhead by sending request bodies as binary WebSocket frames.

### Implementation

#### 1. Update Agent Registration

In `internal/agent/agent.go`, advertise binary protocol support:

```go
func (a *Agent) register() error {
    msg := map[string]interface{}{
        "type": "register",
        "request_id": uuid.New().String(),
        "timestamp": time.Now(),
        "payload": map[string]interface{}{
            "cluster_uuid": a.clusterUUID,
            "agent_id": a.agentID,
            "version": version.GetVersion(),
            "features": map[string]interface{}{
                "supports_streaming": true,
                "supports_binary_protocol": true,  // NEW
                "supports_compression": true,       // NEW (Part 2)
            },
        },
    }
    
    return a.controlPlane.SendMessage(msg)
}
```

#### 2. Handle Binary Protocol in Proxy Requests

Update `internal/agent/proxy.go`:

```go
func (a *Agent) handleProxyRequest(msg Message) {
    payload := msg.Payload
    
    // Extract request details
    method := payload["method"].(string)
    path := payload["path"].(string)
    query := payload["query"].(string)
    headers := parseHeaders(payload["headers"])
    requestID := msg.RequestID
    
    var body []byte
    var err error
    
    // Check if using binary protocol
    if useBinary, ok := payload["body_binary"].(bool); ok && useBinary {
        // Binary protocol: body comes in separate binary frame
        bodySize := int(payload["body_size"].(float64))
        
        a.logger.Debugf("Using binary protocol for request: reqID=%s bodySize=%d", 
            requestID, bodySize)
        
        // Read next binary message
        messageType, binaryFrame, err := a.controlPlane.ReadMessage()
        if err != nil {
            a.sendProxyError(requestID, fmt.Errorf("failed to read binary frame: %w", err))
            return
        }
        
        if messageType != websocket.BinaryMessage {
            a.sendProxyError(requestID, fmt.Errorf("expected binary message, got %d", messageType))
            return
        }
        
        // Parse binary frame: [2 bytes reqID length][reqID][body]
        if len(binaryFrame) < 2 {
            a.sendProxyError(requestID, errors.New("binary frame too short"))
            return
        }
        
        reqIDLen := int(binaryFrame[0])<<8 | int(binaryFrame[1])
        if len(binaryFrame) < 2+reqIDLen {
            a.sendProxyError(requestID, errors.New("invalid binary frame format"))
            return
        }
        
        frameReqID := string(binaryFrame[2:2+reqIDLen])
        if frameReqID != requestID {
            a.sendProxyError(requestID, 
                fmt.Errorf("request ID mismatch: expected %s, got %s", requestID, frameReqID))
            return
        }
        
        body = binaryFrame[2+reqIDLen:]
        
        a.logger.Infof("Received binary protocol request: reqID=%s bodySize=%d (no base64 overhead)", 
            requestID, len(body))
        
    } else {
        // Legacy: base64 encoded body
        if bodyB64, ok := payload["body"].(string); ok && bodyB64 != "" {
            body, err = base64.StdEncoding.DecodeString(bodyB64)
            if err != nil {
                a.sendProxyError(requestID, fmt.Errorf("failed to decode body: %w", err))
                return
            }
            
            a.logger.Debugf("Using base64 protocol for request: reqID=%s", requestID)
        }
    }
    
    // Build K8s request (rest of the function remains the same)
    k8sURL := fmt.Sprintf("https://%s%s", a.k8sClient.GetAPIHost(), path)
    if query != "" {
        k8sURL += "?" + query
    }
    
    req, err := http.NewRequest(method, k8sURL, bytes.NewReader(body))
    if err != nil {
        a.sendProxyError(requestID, err)
        return
    }
    
    // Copy headers and add K8s auth
    for k, v := range headers {
        req.Header[k] = v
    }
    req.Header.Set("Authorization", "Bearer "+a.k8sClient.GetToken())
    
    // Send request to K8s
    resp, err := a.k8sClient.Do(req)
    if err != nil {
        a.sendProxyError(requestID, err)
        return
    }
    defer resp.Body.Close()
    
    // Read response body
    respBody, err := io.ReadAll(resp.Body)
    if err != nil {
        a.sendProxyError(requestID, err)
        return
    }
    
    // Send response (with potential compression - Part 2)
    a.sendProxyResponse(requestID, resp.StatusCode, resp.Header, respBody)
}
```

## Part 2: Compression Support

### Objective

Compress text-based responses (JSON, YAML, text) to reduce bandwidth by 40-60%.

### Implementation

#### 1. Add Compression Utilities

Create `internal/agent/compression.go`:

```go
package agent

import (
    "bytes"
    "compress/gzip"
    "strings"
)

// shouldCompress determines if response should be compressed
func shouldCompress(contentType string, bodySize int) bool {
    // Don't compress small bodies (overhead not worth it)
    if bodySize < 1024 {
        return false
    }
    
    // Don't compress already compressed formats
    if strings.Contains(contentType, "gzip") ||
       strings.Contains(contentType, "compress") ||
       strings.Contains(contentType, "zip") ||
       strings.Contains(contentType, "image/") ||
       strings.Contains(contentType, "video/") ||
       strings.Contains(contentType, "audio/") {
        return false
    }
    
    // Compress text-based formats
    if strings.Contains(contentType, "text/") ||
       strings.Contains(contentType, "json") ||
       strings.Contains(contentType, "javascript") ||
       strings.Contains(contentType, "xml") ||
       strings.Contains(contentType, "html") ||
       strings.Contains(contentType, "yaml") {
        return true
    }
    
    return false
}

// compressData compresses data using gzip
func compressData(data []byte) ([]byte, error) {
    var buf bytes.Buffer
    writer := gzip.NewWriter(&buf)
    
    if _, err := writer.Write(data); err != nil {
        writer.Close()
        return nil, err
    }
    
    if err := writer.Close(); err != nil {
        return nil, err
    }
    
    return buf.Bytes(), nil
}
```

#### 2. Update Proxy Response Handler

Update `internal/agent/proxy.go`:

```go
func (a *Agent) sendProxyResponse(requestID string, status int, headers http.Header, body []byte) {
    // Prepare headers map
    headerMap := make(map[string][]string)
    for k, v := range headers {
        headerMap[k] = v
    }
    
    // Determine if we should compress
    contentType := headers.Get("Content-Type")
    var bodyData string
    var encoding string
    
    if shouldCompress(contentType, len(body)) {
        // Compress the body
        compressed, err := compressData(body)
        if err != nil {
            a.logger.WithError(err).Error("Failed to compress response")
            // Fall back to uncompressed
            bodyData = base64.StdEncoding.EncodeToString(body)
            encoding = "base64"
        } else {
            // Use compressed data
            bodyData = base64.StdEncoding.EncodeToString(compressed)
            encoding = "gzip"
            
            originalSize := len(body)
            compressedSize := len(compressed)
            ratio := float64(originalSize) / float64(compressedSize)
            savings := originalSize - compressedSize
            
            a.logger.Infof("Compressed response: reqID=%s original=%d compressed=%d ratio=%.2fx savings=%d bytes",
                requestID, originalSize, compressedSize, ratio, savings)
        }
    } else {
        // Don't compress
        bodyData = base64.StdEncoding.EncodeToString(body)
        encoding = "base64"
        
        a.logger.Debugf("Response not compressed: reqID=%s size=%d contentType=%s", 
            requestID, len(body), contentType)
    }
    
    // Send response message
    msg := map[string]interface{}{
        "type": "proxy_response",
        "request_id": requestID,
        "timestamp": time.Now(),
        "payload": map[string]interface{}{
            "request_id": requestID,
            "status": status,
            "headers": headerMap,
            "body": bodyData,
            "body_encoding": encoding,  // "base64" or "gzip"
        },
    }
    
    if err := a.controlPlane.SendMessage(msg); err != nil {
        a.logger.WithError(err).Error("Failed to send proxy response")
    }
}
```

## Testing

### Test Binary Protocol

```bash
# Create a pod with large config
kubectl apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: large-config
data:
  data: $(head -c 10000 /dev/urandom | base64)
EOF
```

Expected logs:
```
Received binary protocol request: reqID=... bodySize=... (no base64 overhead)
```

### Test Compression

```bash
# Get JSON response (should be compressed)
kubectl get pods -o json
```

Expected logs:
```
Compressed response: reqID=... original=5000 compressed=1200 ratio=4.17x savings=3800 bytes
```

### Test Small Responses (No Compression)

```bash
# Small response should not be compressed
kubectl get nodes --no-headers | wc -l
```

Expected logs:
```
Response not compressed: reqID=... size=512 contentType=text/plain
```

## Performance Metrics

Add metrics to track:

```go
// In internal/agent/metrics.go
var (
    binaryProtocolRequests = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "agent_binary_protocol_requests_total",
        Help: "Total requests using binary protocol",
    })
    
    compressionRatio = prometheus.NewHistogram(prometheus.HistogramOpts{
        Name: "agent_compression_ratio",
        Help: "Compression ratio for responses",
        Buckets: []float64{1, 2, 3, 4, 5, 10},
    })
    
    bandwidthSaved = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "agent_bandwidth_saved_bytes",
        Help: "Total bytes saved through compression",
    })
)
```

## Rollout Strategy

1. **Phase 1**: Deploy controller changes (already done)
2. **Phase 2**: Deploy agent binary protocol support
3. **Phase 3**: Monitor metrics for 1 week
4. **Phase 4**: Deploy compression support
5. **Phase 5**: Validate bandwidth reduction

## Success Criteria

- [ ] Binary protocol requests handled correctly
- [ ] Compression working for JSON/text responses
- [ ] No increase in error rate
- [ ] Bandwidth reduction: 50-70%
- [ ] Latency improvement: 20-30%
- [ ] No memory leaks from compression
- [ ] Proper fallback to base64 when needed

## Backward Compatibility

- Agent supports both binary and base64 protocols
- Controller detects agent capabilities from registration
- Automatic fallback to base64 for older agents
- Compression is optional (controlled by content-type)

## Security Considerations

- Validate binary frame format before processing
- Limit maximum body size (prevent DoS)
- Implement compression bomb protection
- Log suspicious compression ratios

## Future Enhancements

- [ ] Add zstd compression (better than gzip)
- [ ] Implement streaming compression for large responses
- [ ] Add compression level configuration
- [ ] Implement response caching
- [ ] Add bandwidth quota enforcement

## References

- Controller Binary Protocol: PR #5042
- gzip: Go standard library `compress/gzip`
- WebSocket Binary Frames: RFC 6455 Section 5.6
