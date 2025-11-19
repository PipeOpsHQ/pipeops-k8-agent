package websocket

import (
	"sync"
)

// BufferPool manages a pool of reusable byte buffers for frame encoding/decoding
// This reduces GC pressure and memory allocations
type BufferPool struct {
	pool sync.Pool
}

// NewBufferPool creates a new buffer pool with the specified buffer size
func NewBufferPool(bufferSize int) *BufferPool {
	return &BufferPool{
		pool: sync.Pool{
			New: func() interface{} {
				buf := make([]byte, 0, bufferSize)
				return &buf
			},
		},
	}
}

// Get retrieves a buffer from the pool
func (p *BufferPool) Get() *[]byte {
	return p.pool.Get().(*[]byte)
}

// Put returns a buffer to the pool
func (p *BufferPool) Put(buf *[]byte) {
	if buf != nil {
		// Reset length to 0 but keep capacity
		// Don't clear data, just reset slice bounds
		*buf = (*buf)[:0]
		p.pool.Put(buf)
	}
}

// Global buffer pools for different sizes
var (
	// SmallBufferPool for frames up to 4KB
	SmallBufferPool = NewBufferPool(4 * 1024)

	// MediumBufferPool for frames up to 64KB
	MediumBufferPool = NewBufferPool(64 * 1024)

	// LargeBufferPool for frames up to 1MB
	LargeBufferPool = NewBufferPool(1024 * 1024)
)

// GetBuffer returns an appropriately sized buffer from the pool
func GetBuffer(size int) *[]byte {
	switch {
	case size <= 4*1024:
		return SmallBufferPool.Get()
	case size <= 64*1024:
		return MediumBufferPool.Get()
	default:
		return LargeBufferPool.Get()
	}
}

// PutBuffer returns a buffer to the appropriate pool
func PutBuffer(buf *[]byte, size int) {
	if buf == nil {
		return
	}

	switch {
	case size <= 4*1024:
		SmallBufferPool.Put(buf)
	case size <= 64*1024:
		MediumBufferPool.Put(buf)
	default:
		LargeBufferPool.Put(buf)
	}
}
