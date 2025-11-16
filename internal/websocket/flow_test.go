package websocket

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewFlowController(t *testing.T) {
	cfg := DefaultConfig()
	fc := NewFlowController(cfg)

	assert.NotNil(t, fc)
	assert.Equal(t, cfg.ChannelCapacity, fc.channelCapacity)
	assert.Equal(t, cfg.BackpressureThreshold, fc.threshold)
	assert.Equal(t, cfg.BackpressureWindow, fc.window)
}

func TestFlowControllerUpdateChannelLength(t *testing.T) {
	cfg := DefaultConfig()
	fc := NewFlowController(cfg)

	fc.UpdateChannelLength(50)
	assert.Equal(t, 0.5, fc.GetFillPercentage())

	fc.UpdateChannelLength(80)
	assert.Equal(t, 0.8, fc.GetFillPercentage())
}

func TestFlowControllerCheckBackpressure(t *testing.T) {
	cfg := &Config{
		ChannelCapacity:       100,
		BackpressureThreshold: 0.8,
		BackpressureWindow:    100 * time.Millisecond,
	}
	fc := NewFlowController(cfg)

	// Below threshold - no backpressure
	fc.UpdateChannelLength(50)
	assert.False(t, fc.CheckBackpressure())
	assert.False(t, fc.IsBackpressured())

	// Just over threshold - first time
	fc.UpdateChannelLength(81)
	assert.False(t, fc.CheckBackpressure())
	assert.False(t, fc.IsBackpressured())

	// Wait for window to pass
	time.Sleep(150 * time.Millisecond)

	// Still over threshold - sustained backpressure
	assert.True(t, fc.CheckBackpressure())
	assert.True(t, fc.IsBackpressured())
	assert.Equal(t, 1, fc.GetBackpressureEvents())

	// Drop below threshold - reset
	fc.UpdateChannelLength(50)
	assert.False(t, fc.CheckBackpressure())
	assert.False(t, fc.IsBackpressured())
}

func TestFlowControllerShouldDrop(t *testing.T) {
	cfg := &Config{
		ChannelCapacity:       100,
		BackpressureThreshold: 0.8,
		BackpressureWindow:    1 * time.Second,
	}
	fc := NewFlowController(cfg)

	// Below capacity
	fc.UpdateChannelLength(50)
	assert.False(t, fc.ShouldDrop())

	// At capacity
	fc.UpdateChannelLength(100)
	assert.True(t, fc.ShouldDrop())

	// Over capacity
	fc.UpdateChannelLength(101)
	assert.True(t, fc.ShouldDrop())
}

func TestFlowControllerReset(t *testing.T) {
	cfg := DefaultConfig()
	fc := NewFlowController(cfg)

	fc.UpdateChannelLength(80)
	fc.CheckBackpressure()

	fc.Reset()

	assert.Equal(t, 0, fc.channelLength)
	assert.False(t, fc.IsBackpressured())
	assert.True(t, fc.lastBackpressure.IsZero())
}

func TestFlowControllerGetStats(t *testing.T) {
	cfg := &Config{
		ChannelCapacity:       100,
		BackpressureThreshold: 0.8,
		BackpressureWindow:    50 * time.Millisecond,
	}
	fc := NewFlowController(cfg)

	fc.UpdateChannelLength(80)

	// First check to start backpressure window
	fc.CheckBackpressure()

	// Wait for window to pass
	time.Sleep(60 * time.Millisecond)

	// Second check to trigger sustained backpressure
	fc.CheckBackpressure()

	stats := fc.GetStats()
	assert.Equal(t, 80, stats.ChannelLength)
	assert.Equal(t, 100, stats.ChannelCapacity)
	assert.Equal(t, 0.8, stats.FillPercentage)
	assert.Equal(t, 1, stats.BackpressureEvents)
	assert.True(t, stats.SustainedBackpressure)
}

func TestFlowControllerGetFillPercentageZeroCapacity(t *testing.T) {
	fc := &FlowController{
		channelCapacity: 0,
		channelLength:   10,
	}

	assert.Equal(t, 0.0, fc.GetFillPercentage())
}
