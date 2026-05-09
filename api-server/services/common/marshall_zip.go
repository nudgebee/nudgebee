package common

import (
	"bytes"
	"compress/gzip"
	"fmt"
)

// MarshalAndGzipJSON compresses any data structure into GZIP-compressed JSON.
func MarshalAndGzipJSON(data any) ([]byte, error) {
	// Marshal data to JSON
	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %w", err)
	}

	// Create GZIP buffer
	var buf bytes.Buffer
	gzipWriter := gzip.NewWriter(&buf)

	// Write JSON to GZIP
	if _, err := gzipWriter.Write(jsonData); err != nil {
		return nil, fmt.Errorf("failed to write to gzip: %w", err)
	}

	// Close writer to flush the compressed data
	if err := gzipWriter.Close(); err != nil {
		return nil, fmt.Errorf("failed to close gzip writer: %w", err)
	}

	return buf.Bytes(), nil
}
