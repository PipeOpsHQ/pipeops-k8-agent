package websocket

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDataFrame(t *testing.T) {
	payload := []byte("test data")
	frame := NewDataFrame(payload)

	assert.Equal(t, CurrentProtocolVersion, frame.Version)
	assert.Equal(t, FrameTypeData, frame.Type)
	assert.Equal(t, FlagNone, frame.Flags)
	assert.Equal(t, uint32(len(payload)), frame.Length)
	assert.Equal(t, payload, frame.Payload)
}

func TestNewControlFrame(t *testing.T) {
	payload := []byte("ping")
	frame := NewControlFrame(FrameTypePing, payload)

	assert.Equal(t, CurrentProtocolVersion, frame.Version)
	assert.Equal(t, FrameTypePing, frame.Type)
	assert.Equal(t, uint32(len(payload)), frame.Length)
	assert.Equal(t, payload, frame.Payload)
}

func TestNewErrorFrame(t *testing.T) {
	reason := "backpressure"
	frame := NewErrorFrame(reason)

	assert.Equal(t, CurrentProtocolVersion, frame.Version)
	assert.Equal(t, FrameTypeError, frame.Type)
	assert.Equal(t, uint32(len(reason)), frame.Length)
	assert.Equal(t, []byte(reason), frame.Payload)
}

func TestFrameEncode(t *testing.T) {
	tests := []struct {
		name    string
		frame   *Frame
		wantErr bool
	}{
		{
			name: "data frame with payload",
			frame: &Frame{
				Version: 1,
				Type:    FrameTypeData,
				Flags:   FlagNone,
				Length:  5,
				Payload: []byte("hello"),
			},
			wantErr: false,
		},
		{
			name: "control frame without payload",
			frame: &Frame{
				Version: 1,
				Type:    FrameTypePing,
				Flags:   FlagNone,
				Length:  0,
				Payload: nil,
			},
			wantErr: false,
		},
		{
			name: "length mismatch",
			frame: &Frame{
				Version: 1,
				Type:    FrameTypeData,
				Flags:   FlagNone,
				Length:  10,
				Payload: []byte("hello"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := tt.frame.Encode()
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, FrameHeaderSize+len(tt.frame.Payload), len(encoded))

			// Verify header
			assert.Equal(t, tt.frame.Version, encoded[0])
			assert.Equal(t, tt.frame.Type, encoded[1])
			assert.Equal(t, tt.frame.Flags, encoded[2])
			assert.Equal(t, tt.frame.Reserved, encoded[3])
			assert.Equal(t, tt.frame.Length, binary.BigEndian.Uint32(encoded[4:8]))

			// Verify payload
			if len(tt.frame.Payload) > 0 {
				assert.Equal(t, tt.frame.Payload, encoded[8:])
			}
		})
	}
}

func TestDecodeFrame(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    *Frame
		wantErr bool
	}{
		{
			name: "valid data frame",
			data: []byte{
				1,             // version
				FrameTypeData, // type
				FlagNone,      // flags
				0,             // reserved
				0, 0, 0, 5,    // length (5)
				'h', 'e', 'l', 'l', 'o', // payload
			},
			want: &Frame{
				Version: 1,
				Type:    FrameTypeData,
				Flags:   FlagNone,
				Length:  5,
				Payload: []byte("hello"),
			},
			wantErr: false,
		},
		{
			name: "valid control frame without payload",
			data: []byte{
				1,             // version
				FrameTypePing, // type
				FlagNone,      // flags
				0,             // reserved
				0, 0, 0, 0,    // length (0)
			},
			want: &Frame{
				Version: 1,
				Type:    FrameTypePing,
				Flags:   FlagNone,
				Length:  0,
				Payload: nil,
			},
			wantErr: false,
		},
		{
			name:    "insufficient data for header",
			data:    []byte{1, 2, 3},
			wantErr: true,
		},
		{
			name: "incomplete payload",
			data: []byte{
				1,             // version
				FrameTypeData, // type
				FlagNone,      // flags
				0,             // reserved
				0, 0, 0, 10,   // length (10)
				'h', 'e', 'l', // only 3 bytes
			},
			wantErr: true,
		},
		{
			name: "unsupported version",
			data: []byte{
				99,            // unsupported version
				FrameTypeData, // type
				FlagNone,      // flags
				0,             // reserved
				0, 0, 0, 0,    // length (0)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeFrame(tt.data)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want.Version, got.Version)
			assert.Equal(t, tt.want.Type, got.Type)
			assert.Equal(t, tt.want.Flags, got.Flags)
			assert.Equal(t, tt.want.Length, got.Length)
			assert.Equal(t, tt.want.Payload, got.Payload)
		})
	}
}

func TestReadFrame(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    *Frame
		wantErr bool
	}{
		{
			name: "valid frame",
			data: []byte{
				1,             // version
				FrameTypeData, // type
				FlagNone,      // flags
				0,             // reserved
				0, 0, 0, 5,    // length (5)
				'h', 'e', 'l', 'l', 'o', // payload
			},
			want: &Frame{
				Version: 1,
				Type:    FrameTypeData,
				Flags:   FlagNone,
				Length:  5,
				Payload: []byte("hello"),
			},
			wantErr: false,
		},
		{
			name:    "EOF on header",
			data:    []byte{1, 2},
			wantErr: true,
		},
		{
			name: "EOF on payload",
			data: []byte{
				1,             // version
				FrameTypeData, // type
				FlagNone,      // flags
				0,             // reserved
				0, 0, 0, 10,   // length (10)
				'h', 'e',      // only 2 bytes
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := bytes.NewReader(tt.data)
			got, err := ReadFrame(r)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want.Version, got.Version)
			assert.Equal(t, tt.want.Type, got.Type)
			assert.Equal(t, tt.want.Flags, got.Flags)
			assert.Equal(t, tt.want.Length, got.Length)
			assert.Equal(t, tt.want.Payload, got.Payload)
		})
	}
}

func TestFrameRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
	}{
		{"empty payload", nil},
		{"small payload", []byte("hello world")},
		{"medium payload", bytes.Repeat([]byte("test"), 256)},
		{"large payload", bytes.Repeat([]byte("x"), 10000)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create frame
			original := NewDataFrame(tt.payload)

			// Encode
			encoded, err := original.Encode()
			require.NoError(t, err)

			// Decode
			decoded, err := DecodeFrame(encoded)
			require.NoError(t, err)

			// Verify
			assert.Equal(t, original.Version, decoded.Version)
			assert.Equal(t, original.Type, decoded.Type)
			assert.Equal(t, original.Flags, decoded.Flags)
			assert.Equal(t, original.Length, decoded.Length)
			
			// Handle nil vs empty slice comparison
			if len(original.Payload) == 0 && len(decoded.Payload) == 0 {
				// Both empty, consider equal
			} else {
				assert.Equal(t, original.Payload, decoded.Payload)
			}
		})
	}
}

func TestFrameWriteToReadFrom(t *testing.T) {
	payload := []byte("test data for write/read")
	frame := NewDataFrame(payload)

	// Write to buffer
	var buf bytes.Buffer
	n, err := frame.WriteTo(&buf)
	require.NoError(t, err)
	assert.Equal(t, int64(FrameHeaderSize+len(payload)), n)

	// Read from buffer
	readFrame, err := ReadFrame(&buf)
	require.NoError(t, err)

	assert.Equal(t, frame.Version, readFrame.Version)
	assert.Equal(t, frame.Type, readFrame.Type)
	assert.Equal(t, frame.Flags, readFrame.Flags)
	assert.Equal(t, frame.Length, readFrame.Length)
	assert.Equal(t, frame.Payload, readFrame.Payload)
}

func TestFrameIsControl(t *testing.T) {
	tests := []struct {
		frameType  byte
		isControl  bool
	}{
		{FrameTypeData, false},
		{FrameTypePing, true},
		{FrameTypePong, true},
		{FrameTypeClose, true},
		{FrameTypeError, true},
		{FrameTypeControl, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.frameType), func(t *testing.T) {
			frame := &Frame{Type: tt.frameType}
			assert.Equal(t, tt.isControl, frame.IsControl())
			assert.Equal(t, !tt.isControl, frame.IsData())
		})
	}
}

func TestFrameFlags(t *testing.T) {
	frame := &Frame{Flags: FlagNone}

	// Test setting flags
	assert.False(t, frame.HasFlag(FlagCompressed))
	frame.SetFlag(FlagCompressed)
	assert.True(t, frame.HasFlag(FlagCompressed))

	// Test multiple flags
	frame.SetFlag(FlagBackpressure)
	assert.True(t, frame.HasFlag(FlagCompressed))
	assert.True(t, frame.HasFlag(FlagBackpressure))
	assert.False(t, frame.HasFlag(FlagFragmented))

	// Test clearing flags
	frame.ClearFlag(FlagCompressed)
	assert.False(t, frame.HasFlag(FlagCompressed))
	assert.True(t, frame.HasFlag(FlagBackpressure))
}

func TestFrameString(t *testing.T) {
	tests := []struct {
		name     string
		frame    *Frame
		contains string
	}{
		{
			name:     "data frame",
			frame:    &Frame{Version: 1, Type: FrameTypeData, Length: 100},
			contains: "DATA",
		},
		{
			name:     "ping frame",
			frame:    &Frame{Version: 1, Type: FrameTypePing, Length: 0},
			contains: "PING",
		},
		{
			name:     "error frame",
			frame:    &Frame{Version: 1, Type: FrameTypeError, Length: 10},
			contains: "ERROR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			str := tt.frame.String()
			assert.Contains(t, str, tt.contains)
		})
	}
}

func TestFrameBoundaries(t *testing.T) {
	// Test max uint32 length (should not panic)
	frame := &Frame{
		Version: 1,
		Type:    FrameTypeData,
		Length:  0,
		Payload: nil,
	}
	
	_, err := frame.Encode()
	assert.NoError(t, err)

	// Test truncated frame detection
	data := []byte{
		1,             // version
		FrameTypeData, // type
		FlagNone,      // flags
		0,             // reserved
		255, 255, 255, 255, // max uint32
	}
	_, err = DecodeFrame(data)
	assert.Error(t, err) // Should fail due to insufficient data
}

func BenchmarkFrameEncode(b *testing.B) {
	payload := bytes.Repeat([]byte("x"), 1024)
	frame := NewDataFrame(payload)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = frame.Encode()
	}
}

func BenchmarkFrameDecode(b *testing.B) {
	payload := bytes.Repeat([]byte("x"), 1024)
	frame := NewDataFrame(payload)
	encoded, _ := frame.Encode()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = DecodeFrame(encoded)
	}
}

func BenchmarkFrameRoundTrip(b *testing.B) {
	payload := bytes.Repeat([]byte("x"), 1024)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame := NewDataFrame(payload)
		encoded, _ := frame.Encode()
		_, _ = DecodeFrame(encoded)
	}
}

func BenchmarkReadWriteFrame(b *testing.B) {
	payload := bytes.Repeat([]byte("x"), 1024)
	frame := NewDataFrame(payload)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		_, _ = frame.WriteTo(&buf)
		_, _ = ReadFrame(&buf)
	}
}

// Test that we handle io.EOF correctly
func TestReadFrameEOF(t *testing.T) {
	r := bytes.NewReader([]byte{})
	_, err := ReadFrame(r)
	assert.ErrorIs(t, err, io.EOF, "should wrap io.EOF error")
}
