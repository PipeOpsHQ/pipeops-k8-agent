package websocket

import (
	"sync"
	"time"
)

// FlowController manages backpressure for WebSocket streams
type FlowController struct {
	channelCapacity int
	threshold       float64
	window          time.Duration

	mu                  sync.RWMutex
	channelLength       int
	lastBackpressure    time.Time
	backpressureEvents  int
	sustainedBackpressure bool
}

// NewFlowController creates a new flow controller
func NewFlowController(config *Config) *FlowController {
	return &FlowController{
		channelCapacity: config.ChannelCapacity,
		threshold:       config.BackpressureThreshold,
		window:          config.BackpressureWindow,
	}
}

// UpdateChannelLength updates the current channel length
func (f *FlowController) UpdateChannelLength(length int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.channelLength = length
}

// CheckBackpressure checks if backpressure is occurring
// Returns true if channel is consistently over threshold
func (f *FlowController) CheckBackpressure() bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Calculate current fill percentage
	fillPercentage := float64(f.channelLength) / float64(f.channelCapacity)

	// Check if over threshold
	if fillPercentage >= f.threshold {
		now := time.Now()

		// If this is first time over threshold, record it
		if f.lastBackpressure.IsZero() {
			f.lastBackpressure = now
			f.sustainedBackpressure = false
			return false
		}

		// Check if sustained for the window duration
		if now.Sub(f.lastBackpressure) >= f.window {
			f.sustainedBackpressure = true
			f.backpressureEvents++
			return true
		}

		return false
	}

	// Below threshold - reset
	f.lastBackpressure = time.Time{}
	f.sustainedBackpressure = false
	return false
}

// IsBackpressured returns true if currently experiencing sustained backpressure
func (f *FlowController) IsBackpressured() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.sustainedBackpressure
}

// GetBackpressureEvents returns the total number of backpressure events
func (f *FlowController) GetBackpressureEvents() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.backpressureEvents
}

// GetFillPercentage returns the current fill percentage
func (f *FlowController) GetFillPercentage() float64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.channelCapacity == 0 {
		return 0
	}
	return float64(f.channelLength) / float64(f.channelCapacity)
}

// Reset resets the flow controller state
func (f *FlowController) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.channelLength = 0
	f.lastBackpressure = time.Time{}
	f.sustainedBackpressure = false
}

// ShouldDrop returns true if the message should be dropped due to backpressure
// This is a simpler check than CheckBackpressure for immediate decisions
func (f *FlowController) ShouldDrop() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.channelLength >= f.channelCapacity
}

// Stats returns flow control statistics
type FlowStats struct {
	ChannelLength         int
	ChannelCapacity       int
	FillPercentage        float64
	BackpressureEvents    int
	SustainedBackpressure bool
}

// GetStats returns current flow control statistics
func (f *FlowController) GetStats() FlowStats {
	f.mu.RLock()
	defer f.mu.RUnlock()

	fillPercentage := 0.0
	if f.channelCapacity > 0 {
		fillPercentage = float64(f.channelLength) / float64(f.channelCapacity)
	}

	return FlowStats{
		ChannelLength:         f.channelLength,
		ChannelCapacity:       f.channelCapacity,
		FillPercentage:        fillPercentage,
		BackpressureEvents:    f.backpressureEvents,
		SustainedBackpressure: f.sustainedBackpressure,
	}
}
