# Agent Status Changes Too Frequently - Root Cause Analysis

## Problem Statement

**Issue:** When the controller is redeployed, the agent status changes to unhealthy and eventually recovers. This happens **too frequently** and looks unstable.

**User Experience:**
- Controller redeploys (common during development/updates)
- Agent shows as unhealthy for 30-60 seconds
- Agent recovers automatically
- Repeat on every controller deployment

## Root Cause Analysis

### 1. Heartbeat Updates State on Every Attempt ⚠️

**Location:** `internal/agent/agent.go:986-991`

```go
case <-ticker.C:
    if err := a.sendHeartbeatWithRetry(); err != nil {
        a.updateConnectionState(StateDisconnected)  // Changes state!
    } else {
        a.updateConnectionState(StateConnected)     // Changes state!
    }
```

**Problem:**
- Every heartbeat (every 30s) updates connection state
- Even if state hasn't actually changed
- Generates excessive state change logs
- No grace period for transient failures

### 2. Heartbeat Fails During Reconnection Window

**Scenario:** Controller redeploys

```text
t=0s:   Controller starts redeploying
t=1s:   Agent detects WebSocket disconnect
        → reconnect() starts (exponential backoff)

t=30s:  ⚠️ Heartbeat timer fires
        → sendHeartbeat() called
        → WebSocket not yet reconnected → FAILS
        → updateConnectionState(StateDisconnected)
        → Control plane: "agent unhealthy"

t=45s:  WebSocket reconnects successfully
        → Re-registration completes

t=60s:  Next heartbeat attempt → SUCCESS
        → updateConnectionState(StateConnected)
        → Control plane: "agent healthy"

Result: 30 seconds of "unhealthy" status
```

### 3. No Grace Period for Failures

**Current Behavior:**
- First heartbeat failure → immediately marks StateDisconnected
- No retry buffer or grace period
- Agent **knows** it's reconnecting but still tries heartbeat

**Should Be:**
- Skip heartbeat if actively reconnecting
- Or allow 2-3 failures before changing state
- Or wait for retry logic to complete first

### 4. Health Endpoints Don't Reflect Reality

**Location:** `internal/server/server.go:180-197`

```go
func (s *Server) handleHealth(c *gin.Context) {
    c.JSON(http.StatusOK, gin.H{"status": "healthy"})  // Always OK!
}

func (s *Server) handleReady(c *gin.Context) {
    c.JSON(http.StatusOK, gin.H{"status": "ready"})    // Always ready!
}
```

**Problem:**
- Kubernetes probes always return 200 OK
- Even when disconnected from control plane
- K8s thinks pod is healthy, but agent can't communicate
- Misleading status

### 5. Control Plane Tracks Last Heartbeat

The control plane marks agents unhealthy based on:
- `last_seen` timestamp from heartbeat
- If no heartbeat for >60s → unhealthy

**Timeline from Control Plane Perspective:**

```text
Normal operation:
  t=0s:   Heartbeat received → healthy (last_seen = 0s ago)
  t=30s:  Heartbeat received → healthy (last_seen = 0s ago)
  t=60s:  Heartbeat received → healthy (last_seen = 0s ago)

During controller redeploy:
  t=0s:   Heartbeat received → healthy
  t=30s:  ⚠️ No heartbeat (agent reconnecting)
          → last_seen = 30s ago → still healthy
  t=60s:  Heartbeat received → healthy
  
  Gap: 30-60 seconds without heartbeat
  Control plane may mark unhealthy if gap >60s
```

## Why It Happens "Too Frequently"

### Frequency Analysis

**Common Trigger Events:**
1. Controller deployments (CI/CD updates)
2. Controller config changes
3. Controller pod restarts
4. Controller node failures
5. Network hiccups
6. Control plane scaling events

**In Active Development:**
- Could be 10-20 controller deployments per day
- Each causes 30-60s unhealthy window
- Total: 5-20 minutes of unhealthy status per day
- Looks very unstable!

**In Production:**
- Could be 3-5 controller deployments per day
- Each causes 30-60s unhealthy window
- Total: 2-5 minutes of unhealthy per day
- Still noticeable!

### Impact of Recent Reconnection Improvements

**Before (60s max backoff):**
```
Controller redeploys at t=0
t=1s:   Disconnect detected
t=30s:  Heartbeat fails → unhealthy
t=125s: Reconnect + heartbeat success → healthy

Unhealthy duration: 95 seconds
```

**After (15s max backoff - current):**
```
Controller redeploys at t=0
t=1s:   Disconnect detected
t=30s:  Heartbeat fails → unhealthy
t=48s:  Reconnect + heartbeat success → healthy

Unhealthy duration: 18 seconds
```

**Improvement:** 81% faster recovery!

**But:** Still shows unhealthy on EVERY controller redeploy

## Recommended Solutions

### Solution 1: Skip Heartbeat During Reconnection 🔥

**Priority:** HIGH (Quick fix, low risk)

**Change:** Don't send heartbeat if actively reconnecting

```go
// internal/agent/agent.go:986
case <-ticker.C:
    // Skip heartbeat if WebSocket is reconnecting
    if a.controlPlane != nil && !a.controlPlane.IsConnected() {
        a.logger.Debug("Skipping heartbeat - WebSocket reconnecting")
        continue
    }
    
    if err := a.sendHeartbeatWithRetry(); err != nil {
        a.updateConnectionState(StateDisconnected)
    } else {
        a.updateConnectionState(StateConnected)
    }
```

**Impact:**
- No heartbeat failures during known reconnection windows
- Agent stays in StateReconnecting (not StateDisconnected)
- Cleaner state transitions
- Still reports unhealthy to control plane (no heartbeat sent)

**Trade-off:**
- Control plane still sees gap in heartbeats
- But agent doesn't thrash between states

### Solution 2: Add Grace Period (2-3 Failures) 🎯

**Priority:** HIGH (Better user experience)

**Change:** Allow multiple failures before marking disconnected

```go
// Add to Agent struct
consecutiveHeartbeatFailures int
heartbeatFailureThreshold    int // Default: 3

// In startHeartbeat()
case <-ticker.C:
    if err := a.sendHeartbeatWithRetry(); err != nil {
        a.consecutiveHeartbeatFailures++
        if a.consecutiveHeartbeatFailures >= a.heartbeatFailureThreshold {
            a.updateConnectionState(StateDisconnected)
        } else {
            a.logger.WithFields(logrus.Fields{
                "failures":  a.consecutiveHeartbeatFailures,
                "threshold": a.heartbeatFailureThreshold,
            }).Warn("Heartbeat failed, within threshold")
        }
    } else {
        a.consecutiveHeartbeatFailures = 0  // Reset on success
        a.updateConnectionState(StateConnected)
    }
```

**Impact:**
- Brief reconnections (<90s) won't trigger unhealthy
- Longer outages still detected
- More stable status reporting

**Trade-off:**
- Slower detection of actual failures
- But better for transient issues

### Solution 3: Make Health Endpoints Truthful ✅

**Priority:** MEDIUM (Better K8s integration)

**Change:** Health endpoint should check actual connectivity

```go
// internal/server/server.go
func (s *Server) handleReady(c *gin.Context) {
    // Check if agent is actually connected to control plane
    ready := true
    reasons := []string{}
    
    if s.agentStatus != nil {
        if !s.agentStatus.Connected {
            ready = false
            reasons = append(reasons, "not connected to control plane")
        }
        
        // Check last heartbeat time
        if time.Since(s.agentStatus.LastHeartbeat) > 90*time.Second {
            ready = false
            reasons = append(reasons, "heartbeat stale")
        }
    }
    
    if ready {
        c.JSON(http.StatusOK, gin.H{
            "status": "ready",
            "timestamp": time.Now(),
        })
    } else {
        c.JSON(http.StatusServiceUnavailable, gin.H{
            "status": "not ready",
            "reasons": reasons,
            "timestamp": time.Now(),
        })
    }
}
```

**Impact:**
- K8s will restart pod if truly disconnected
- Better reflects actual agent state
- Service endpoints respect connectivity

**Trade-off:**
- Pod restarts during controller redeploys
- May be too aggressive for transient issues
- Need careful tuning of failureThreshold

### Solution 4: Only Update State When Changed 📊

**Priority:** LOW (Nice to have, reduces log noise)

**Change:** Don't update state if it hasn't changed

```go
// internal/agent/agent.go:953
func (a *Agent) updateConnectionState(newState ConnectionState) {
    a.stateMutex.Lock()
    oldState := a.connectionState
    
    // Only update if state actually changed
    if oldState == newState {
        a.stateMutex.Unlock()
        return  // No change, skip logging
    }
    
    a.connectionState = newState
    a.stateMutex.Unlock()

    a.logger.WithFields(logrus.Fields{
        "old_state": oldState.String(),
        "new_state": newState.String(),
    }).Info("Connection state changed")
}
```

**Impact:**
- Cleaner logs (no redundant "state changed" messages)
- Less CPU/memory overhead
- Easier to spot real state transitions

**Already Implemented:** Check line 959 - this is already done!

### Solution 5: Smarter Heartbeat Scheduling 🧠

**Priority:** MEDIUM (More sophisticated)

**Change:** Adjust heartbeat timing based on connection state

```go
// Use different intervals for different states
func (a *Agent) startHeartbeat() {
    for {
        var interval time.Duration
        
        state := a.getConnectionState()
        switch state {
        case StateConnected:
            interval = 30 * time.Second
        case StateReconnecting:
            interval = 60 * time.Second  // Less frequent during reconnection
        case StateDisconnected:
            interval = 10 * time.Second  // More frequent when trying to recover
        }
        
        select {
        case <-time.After(interval):
            // Send heartbeat
        case <-a.ctx.Done():
            return
        }
    }
}
```

**Impact:**
- Reduces unnecessary heartbeat attempts during reconnection
- More aggressive recovery attempts when disconnected
- Adaptive behavior based on state

## Recommended Implementation Plan

### Phase 1: Quick Wins (1-2 hours) 🔥

1. **Skip heartbeat during reconnection** (Solution 1)
   - Check WebSocket connection state
   - Skip heartbeat if reconnecting
   - Log skipped attempts

2. **Add grace period** (Solution 2)
   - Allow 2-3 failures before marking disconnected
   - Reset counter on success
   - Configurable threshold

**Expected Impact:** 80%+ reduction in false unhealthy status

### Phase 2: Better Integration (2-4 hours)

3. **Make health endpoints truthful** (Solution 3)
   - Check actual connectivity in `/ready`
   - Keep `/health` simple (pod is running)
   - Tune K8s probe thresholds

4. **Smarter heartbeat scheduling** (Solution 5)
   - Adjust interval based on state
   - Less frequent during reconnection
   - More frequent when recovering

**Expected Impact:** More stable K8s integration, better UX

### Phase 3: Polish (Optional)

5. Add metrics for monitoring:
   - `agent_heartbeat_failures_total`
   - `agent_connection_state_changes_total`
   - `agent_unhealthy_duration_seconds`

6. Add configuration options:
   - `heartbeat_failure_threshold`
   - `heartbeat_skip_during_reconnect`
   - `health_check_connectivity`

## Testing Strategy

### Test 1: Controller Redeploy

```bash
# Redeploy controller
kubectl rollout restart deployment controller -n pipeops

# Watch agent status
kubectl logs -f pipeops-agent-xxx -n pipeops | grep -E "state|heartbeat"

# Expected: No StateDisconnected during reconnection
```

### Test 2: Extended Outage

```bash
# Block control plane
sudo iptables -A OUTPUT -d <control-plane-ip> -j DROP

# Wait 5 minutes
sleep 300

# Restore
sudo iptables -D OUTPUT -d <control-plane-ip> -j DROP

# Expected: StateDisconnected after threshold reached
```

### Test 3: Rapid Reconnections

```bash
# Repeatedly restart controller
for i in {1..10}; do
  kubectl rollout restart deployment controller -n pipeops
  sleep 60
done

# Expected: Stable state, minimal thrashing
```

## Success Criteria

### Before Changes
- Controller redeploy → unhealthy for 30-60s (100% of the time)
- 10 redeploys/day → 5-10 min unhealthy/day
- Logs full of state transitions

### After Phase 1
- Controller redeploy → stays in StateReconnecting (not StateDisconnected)
- Unhealthy only if reconnection takes >90s (grace period)
- Reduced state transitions by 80%+

### After Phase 2
- K8s probes reflect actual connectivity
- Smarter heartbeat scheduling
- Better observability with metrics

## Summary

**Root Cause:** Heartbeat attempts during known reconnection windows cause false "unhealthy" status.

**Quick Fix (Phase 1):**
1. Skip heartbeat if WebSocket reconnecting
2. Add 2-3 failure grace period

**Impact:** 80%+ reduction in false unhealthy status with minimal code changes.

**Implementation Time:** 1-2 hours for Phase 1.

**Risk:** LOW - Only changes heartbeat logic, no protocol changes.
