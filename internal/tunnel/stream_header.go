package tunnel

import (
	"encoding/binary"
	"io"
)

const (
	// TunnelStreamHeaderVersion is the current version of the tunnel stream header protocol
	TunnelStreamHeaderVersion uint8 = 1

	// TunnelProtocolTCP indicates a TCP tunnel stream
	TunnelProtocolTCP uint8 = 1

	// TunnelProtocolUDP indicates a UDP tunnel stream
	TunnelProtocolUDP uint8 = 2
)

// TunnelStreamHeader is sent by the gateway at the start of each yamux stream
// It contains routing information to identify the target service
type TunnelStreamHeader struct {
	Version          uint8
	Protocol         uint8
	ServicePort      uint16
	ServiceName      string
	ServiceNamespace string
	TunnelID         string
}

// ReadFrom reads the header from a stream
func (h *TunnelStreamHeader) ReadFrom(r io.Reader) error {
	if err := binary.Read(r, binary.BigEndian, &h.Version); err != nil {
		return err
	}
	if err := binary.Read(r, binary.BigEndian, &h.Protocol); err != nil {
		return err
	}
	if err := binary.Read(r, binary.BigEndian, &h.ServicePort); err != nil {
		return err
	}

	var err error
	h.ServiceName, err = readString(r)
	if err != nil {
		return err
	}
	h.ServiceNamespace, err = readString(r)
	if err != nil {
		return err
	}
	h.TunnelID, err = readString(r)
	return err
}

// WriteTo writes the header to a stream
func (h *TunnelStreamHeader) WriteTo(w io.Writer) error {
	if err := binary.Write(w, binary.BigEndian, h.Version); err != nil {
		return err
	}
	if err := binary.Write(w, binary.BigEndian, h.Protocol); err != nil {
		return err
	}
	if err := binary.Write(w, binary.BigEndian, h.ServicePort); err != nil {
		return err
	}

	if err := writeString(w, h.ServiceName); err != nil {
		return err
	}
	if err := writeString(w, h.ServiceNamespace); err != nil {
		return err
	}
	return writeString(w, h.TunnelID)
}

// readString reads a length-prefixed string from the reader
func readString(r io.Reader) (string, error) {
	var length uint16
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return "", err
	}
	if length == 0 {
		return "", nil
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// writeString writes a length-prefixed string to the writer
func writeString(w io.Writer, s string) error {
	length := uint16(len(s))
	if err := binary.Write(w, binary.BigEndian, length); err != nil {
		return err
	}
	if length > 0 {
		_, err := w.Write([]byte(s))
		return err
	}
	return nil
}
