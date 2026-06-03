package workflow

import (
	"bytes"
	"compress/zlib"
	"io"

	commonv1 "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
)

type compressionCodec struct {
	minSize int
}

// NewCompressionCodec creates a new compression codec that compresses payloads larger than minSize.
// If minSize is 0, a default of 128 bytes is used.
func NewCompressionCodec(minSize int) converter.PayloadCodec {
	if minSize <= 0 {
		minSize = 128
	}
	return &compressionCodec{
		minSize: minSize,
	}
}

func (c *compressionCodec) Encode(payloads []*commonv1.Payload) ([]*commonv1.Payload, error) {
	result := make([]*commonv1.Payload, len(payloads))
	for i, p := range payloads {
		if len(p.GetData()) < c.minSize {
			result[i] = p
			continue
		}

		var buf bytes.Buffer
		w := zlib.NewWriter(&buf)
		if _, err := w.Write(p.GetData()); err != nil {
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}

		result[i] = &commonv1.Payload{
			Metadata: map[string][]byte{
				converter.MetadataEncoding: []byte("binary/zlib"),
			},
			Data: buf.Bytes(),
		}
		// Preserve original metadata except encoding, and save original encoding
		for k, v := range p.GetMetadata() {
			if k == converter.MetadataEncoding {
				result[i].Metadata["original-encoding"] = v
			} else {
				result[i].Metadata[k] = v
			}
		}
	}
	return result, nil
}

func (c *compressionCodec) Decode(payloads []*commonv1.Payload) ([]*commonv1.Payload, error) {
	result := make([]*commonv1.Payload, len(payloads))
	for i, p := range payloads {
		if string(p.GetMetadata()[converter.MetadataEncoding]) != "binary/zlib" {
			result[i] = p
			continue
		}

		r, err := zlib.NewReader(bytes.NewReader(p.GetData()))
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		if err := r.Close(); err != nil {
			return nil, err
		}

		result[i] = &commonv1.Payload{
			Metadata: make(map[string][]byte),
			Data:     data,
		}
		// Restore original metadata
		for k, v := range p.GetMetadata() {
			if k == "original-encoding" {
				result[i].Metadata[converter.MetadataEncoding] = v
			} else if k != converter.MetadataEncoding {
				result[i].Metadata[k] = v
			}
		}
	}
	return result, nil
}
