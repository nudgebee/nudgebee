package common

import (
	"bytes"
	"compress/gzip"
	stdjson "encoding/json"
	"io"
	"reflect"
	"testing"
)

// gunzip is a test helper that decompresses gzip bytes.
func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer func() { _ = r.Close() }()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading gzip: %v", err)
	}
	return out
}

func TestMarshalAndGzipJSON_RoundTrip(t *testing.T) {
	// JSON numbers decode back as float64, so use float64 in the expectation.
	in := map[string]any{"name": "alpha", "count": float64(3)}

	got, err := MarshalAndGzipJSON(in)
	if err != nil {
		t.Fatalf("MarshalAndGzipJSON() error = %v", err)
	}

	var out map[string]any
	if err := stdjson.Unmarshal(gunzip(t, got), &out); err != nil {
		t.Fatalf("unmarshal decompressed JSON: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round-trip mismatch: got %v, want %v", out, in)
	}
}

func TestMarshalAndGzipJSON_GzipMagicHeader(t *testing.T) {
	got, err := MarshalAndGzipJSON(map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("MarshalAndGzipJSON() error = %v", err)
	}
	if len(got) < 2 || got[0] != 0x1f || got[1] != 0x8b {
		t.Errorf("output is not gzip framed: bytes = %x", got)
	}
}

func TestMarshalAndGzipJSON_Struct(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
		N    int    `json:"n"`
	}
	in := payload{Name: "x", N: 7}

	got, err := MarshalAndGzipJSON(in)
	if err != nil {
		t.Fatalf("MarshalAndGzipJSON() error = %v", err)
	}

	var out payload
	if err := stdjson.Unmarshal(gunzip(t, got), &out); err != nil {
		t.Fatalf("unmarshal decompressed JSON: %v", err)
	}
	if out != in {
		t.Errorf("round-trip mismatch: got %+v, want %+v", out, in)
	}
}

func TestMarshalAndGzipJSON_MarshalError(t *testing.T) {
	// Channels can't be marshaled to JSON; the error must propagate.
	if _, err := MarshalAndGzipJSON(make(chan int)); err == nil {
		t.Error("expected error marshaling an unsupported type (channel), got nil")
	}
}
