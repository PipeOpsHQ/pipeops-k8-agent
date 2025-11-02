package controlplane

import (
	"bytes"
	"compress/gzip"
	"strings"
)

// shouldCompress determines if response should be compressed based on content type and size
func shouldCompress(contentType string, bodySize int) bool {
	// Don't compress small bodies (< 1KB)
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
