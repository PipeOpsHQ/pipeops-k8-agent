package websocket

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBufferPool(t *testing.T) {
	pool := NewBufferPool(1024)

	// Get a buffer
	buf1 := pool.Get()
	assert.NotNil(t, buf1)
	assert.Equal(t, 1024, cap(*buf1))
	
	// Initially buffer length should be either 0 or the capacity
	// depending on whether it's newly allocated or reused

	// Modify it
	*buf1 = append((*buf1)[:0], []byte("test data")...)
	assert.Equal(t, 9, len(*buf1))

	// Return to pool
	pool.Put(buf1)

	// Get another buffer (might be the same one)
	buf2 := pool.Get()
	assert.NotNil(t, buf2)
	// Buffer should be cleared when returned to pool (length reset to 0)
	assert.Equal(t, 0, len(*buf2))
	assert.Equal(t, 1024, cap(*buf2))
}

func TestGetBuffer(t *testing.T) {
	tests := []struct {
		name         string
		size         int
		expectedPool string
	}{
		{
			name:         "small buffer",
			size:         1024,
			expectedPool: "small",
		},
		{
			name:         "medium buffer",
			size:         32 * 1024,
			expectedPool: "medium",
		},
		{
			name:         "large buffer",
			size:         512 * 1024,
			expectedPool: "large",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := GetBuffer(tt.size)
			assert.NotNil(t, buf)
			assert.True(t, cap(*buf) >= tt.size)

			// Return it
			PutBuffer(buf, tt.size)
		})
	}
}

func TestPutBufferNil(t *testing.T) {
	// Should not panic
	PutBuffer(nil, 1024)
}

func BenchmarkBufferPoolGet(b *testing.B) {
	pool := NewBufferPool(4096)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := pool.Get()
		pool.Put(buf)
	}
}

func BenchmarkBufferAllocation(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := make([]byte, 4096)
		_ = buf
	}
}
