package tunnel

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTunnelStreamHeader_ReadWrite(t *testing.T) {
	testCases := []struct {
		name   string
		header TunnelStreamHeader
	}{
		{
			name: "TCP tunnel",
			header: TunnelStreamHeader{
				Version:          TunnelStreamHeaderVersion,
				Protocol:         TunnelProtocolTCP,
				ServicePort:      5432,
				ServiceName:      "postgres",
				ServiceNamespace: "databases",
				TunnelID:         "tunnel-abc123",
			},
		},
		{
			name: "UDP tunnel",
			header: TunnelStreamHeader{
				Version:          TunnelStreamHeaderVersion,
				Protocol:         TunnelProtocolUDP,
				ServicePort:      53,
				ServiceName:      "coredns",
				ServiceNamespace: "kube-system",
				TunnelID:         "tunnel-dns-001",
			},
		},
		{
			name: "empty strings",
			header: TunnelStreamHeader{
				Version:          1,
				Protocol:         TunnelProtocolTCP,
				ServicePort:      80,
				ServiceName:      "",
				ServiceNamespace: "",
				TunnelID:         "",
			},
		},
		{
			name: "unicode service name",
			header: TunnelStreamHeader{
				Version:          1,
				Protocol:         TunnelProtocolTCP,
				ServicePort:      8080,
				ServiceName:      "service-测试",
				ServiceNamespace: "namespace-ñ",
				TunnelID:         "tunnel-émoji-🚀",
			},
		},
		{
			name: "max port",
			header: TunnelStreamHeader{
				Version:          1,
				Protocol:         TunnelProtocolTCP,
				ServicePort:      65535,
				ServiceName:      "high-port-svc",
				ServiceNamespace: "default",
				TunnelID:         "tunnel-max-port",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Write to buffer
			var buf bytes.Buffer
			err := tc.header.WriteTo(&buf)
			require.NoError(t, err)

			// Read back
			var decoded TunnelStreamHeader
			err = decoded.ReadFrom(&buf)
			require.NoError(t, err)

			// Compare
			assert.Equal(t, tc.header.Version, decoded.Version)
			assert.Equal(t, tc.header.Protocol, decoded.Protocol)
			assert.Equal(t, tc.header.ServicePort, decoded.ServicePort)
			assert.Equal(t, tc.header.ServiceName, decoded.ServiceName)
			assert.Equal(t, tc.header.ServiceNamespace, decoded.ServiceNamespace)
			assert.Equal(t, tc.header.TunnelID, decoded.TunnelID)
		})
	}
}

func TestTunnelStreamHeader_ReadFromEmpty(t *testing.T) {
	var buf bytes.Buffer
	var header TunnelStreamHeader
	err := header.ReadFrom(&buf)
	assert.Error(t, err) // Should fail on empty buffer
}

func TestTunnelStreamHeader_ReadFromTruncated(t *testing.T) {
	// Write a header
	original := TunnelStreamHeader{
		Version:          1,
		Protocol:         TunnelProtocolTCP,
		ServicePort:      5432,
		ServiceName:      "postgres",
		ServiceNamespace: "databases",
		TunnelID:         "tunnel-123",
	}

	var buf bytes.Buffer
	err := original.WriteTo(&buf)
	require.NoError(t, err)

	// Truncate the buffer
	truncated := buf.Bytes()[:10]

	var header TunnelStreamHeader
	err = header.ReadFrom(bytes.NewReader(truncated))
	assert.Error(t, err) // Should fail on truncated data
}

func TestProtocolConstants(t *testing.T) {
	assert.Equal(t, TunnelProtocolTCP, uint8(1))
	assert.Equal(t, TunnelProtocolUDP, uint8(2))
	assert.Equal(t, TunnelStreamHeaderVersion, uint8(1))
}

func BenchmarkTunnelStreamHeader_WriteRead(b *testing.B) {
	header := TunnelStreamHeader{
		Version:          1,
		Protocol:         TunnelProtocolTCP,
		ServicePort:      5432,
		ServiceName:      "postgres",
		ServiceNamespace: "databases",
		TunnelID:         "tunnel-abc123",
	}

	for i := 0; i < b.N; i++ {
		var buf bytes.Buffer
		_ = header.WriteTo(&buf)
		var decoded TunnelStreamHeader
		_ = decoded.ReadFrom(&buf)
	}
}
