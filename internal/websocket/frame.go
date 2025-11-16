package websocket

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Protocol version constants
const (
	// ProtocolV1 is the legacy JSON-based protocol with base64 encoding
	ProtocolV1 = "v1"
	// ProtocolV2 is the new binary envelope protocol
	ProtocolV2 = "v2"

	// Frame header size (version + type + flags + length)
	FrameHeaderSize = 8

	// Current protocol version (1 byte in header)
	CurrentProtocolVersion byte = 1
)

// Frame type constants
const (
	FrameTypeData    byte = 0x01 // Regular data frame
	FrameTypePing    byte = 0x02 // Ping control frame
	FrameTypePong    byte = 0x03 // Pong control frame
	FrameTypeClose   byte = 0x04 // Close control frame
	FrameTypeError   byte = 0x05 // Error frame with reason
	FrameTypeControl byte = 0x06 // Generic control frame
)

// Frame flags
const (
	FlagNone        byte = 0x00
	FlagCompressed  byte = 0x01 // Data is compressed
	FlagFragmented  byte = 0x02 // Part of fragmented message
	FlagFinalFrame  byte = 0x04 // Final frame in sequence
	FlagBackpressure byte = 0x08 // Backpressure indicator
)

// Frame represents a binary WebSocket frame with envelope
// Format: [1 byte version][1 byte type][1 byte flags][1 byte reserved][4 bytes length][payload]
type Frame struct {
	Version  byte   // Protocol version
	Type     byte   // Frame type
	Flags    byte   // Frame flags
	Reserved byte   // Reserved for future use
	Length   uint32 // Payload length
	Payload  []byte // Frame payload
}

// NewDataFrame creates a new data frame
func NewDataFrame(payload []byte) *Frame {
	return &Frame{
		Version: CurrentProtocolVersion,
		Type:    FrameTypeData,
		Flags:   FlagNone,
		Length:  uint32(len(payload)),
		Payload: payload,
	}
}

// NewControlFrame creates a new control frame
func NewControlFrame(frameType byte, payload []byte) *Frame {
	return &Frame{
		Version: CurrentProtocolVersion,
		Type:    frameType,
		Flags:   FlagNone,
		Length:  uint32(len(payload)),
		Payload: payload,
	}
}

// NewErrorFrame creates a new error frame with a reason
func NewErrorFrame(reason string) *Frame {
	return &Frame{
		Version: CurrentProtocolVersion,
		Type:    FrameTypeError,
		Flags:   FlagNone,
		Length:  uint32(len(reason)),
		Payload: []byte(reason),
	}
}

// Encode encodes the frame to binary format
// Returns the encoded bytes or an error
func (f *Frame) Encode() ([]byte, error) {
	if f.Length != uint32(len(f.Payload)) {
		return nil, fmt.Errorf("frame length mismatch: header=%d, actual=%d", f.Length, len(f.Payload))
	}

	buf := make([]byte, FrameHeaderSize+len(f.Payload))
	buf[0] = f.Version
	buf[1] = f.Type
	buf[2] = f.Flags
	buf[3] = f.Reserved
	binary.BigEndian.PutUint32(buf[4:8], f.Length)
	copy(buf[8:], f.Payload)

	return buf, nil
}

// WriteTo writes the frame to a writer
func (f *Frame) WriteTo(w io.Writer) (int64, error) {
	encoded, err := f.Encode()
	if err != nil {
		return 0, fmt.Errorf("encode frame: %w", err)
	}

	n, err := w.Write(encoded)
	return int64(n), err
}

// DecodeFrame decodes a frame from binary data
func DecodeFrame(data []byte) (*Frame, error) {
	if len(data) < FrameHeaderSize {
		return nil, fmt.Errorf("insufficient data for frame header: got %d bytes, need %d", len(data), FrameHeaderSize)
	}

	f := &Frame{
		Version:  data[0],
		Type:     data[1],
		Flags:    data[2],
		Reserved: data[3],
		Length:   binary.BigEndian.Uint32(data[4:8]),
	}

	// Validate version
	if f.Version != CurrentProtocolVersion {
		return nil, fmt.Errorf("unsupported protocol version: %d (expected %d)", f.Version, CurrentProtocolVersion)
	}

	// Check if we have complete payload
	totalSize := FrameHeaderSize + int(f.Length)
	if len(data) < totalSize {
		return nil, fmt.Errorf("incomplete frame: got %d bytes, need %d", len(data), totalSize)
	}

	// Extract payload
	if f.Length > 0 {
		f.Payload = make([]byte, f.Length)
		copy(f.Payload, data[8:totalSize])
	}

	return f, nil
}

// ReadFrame reads a frame from a reader
func ReadFrame(r io.Reader) (*Frame, error) {
	// Read header
	header := make([]byte, FrameHeaderSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, fmt.Errorf("read frame header: %w", err)
	}

	f := &Frame{
		Version:  header[0],
		Type:     header[1],
		Flags:    header[2],
		Reserved: header[3],
		Length:   binary.BigEndian.Uint32(header[4:8]),
	}

	// Validate version
	if f.Version != CurrentProtocolVersion {
		return nil, fmt.Errorf("unsupported protocol version: %d (expected %d)", f.Version, CurrentProtocolVersion)
	}

	// Read payload if present
	if f.Length > 0 {
		f.Payload = make([]byte, f.Length)
		if _, err := io.ReadFull(r, f.Payload); err != nil {
			return nil, fmt.Errorf("read frame payload: %w", err)
		}
	}

	return f, nil
}

// IsControl returns true if this is a control frame
func (f *Frame) IsControl() bool {
	return f.Type == FrameTypePing ||
		f.Type == FrameTypePong ||
		f.Type == FrameTypeClose ||
		f.Type == FrameTypeError ||
		f.Type == FrameTypeControl
}

// IsData returns true if this is a data frame
func (f *Frame) IsData() bool {
	return f.Type == FrameTypeData
}

// HasFlag returns true if the specified flag is set
func (f *Frame) HasFlag(flag byte) bool {
	return (f.Flags & flag) != 0
}

// SetFlag sets the specified flag
func (f *Frame) SetFlag(flag byte) {
	f.Flags |= flag
}

// ClearFlag clears the specified flag
func (f *Frame) ClearFlag(flag byte) {
	f.Flags &^= flag
}

// String returns a string representation of the frame
func (f *Frame) String() string {
	typeName := "UNKNOWN"
	switch f.Type {
	case FrameTypeData:
		typeName = "DATA"
	case FrameTypePing:
		typeName = "PING"
	case FrameTypePong:
		typeName = "PONG"
	case FrameTypeClose:
		typeName = "CLOSE"
	case FrameTypeError:
		typeName = "ERROR"
	case FrameTypeControl:
		typeName = "CONTROL"
	}

	return fmt.Sprintf("Frame{Version=%d, Type=%s, Flags=0x%02x, Length=%d}",
		f.Version, typeName, f.Flags, f.Length)
}
