package gcloud

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNormalizeGCPLocation guards nb-40328: GCP's own APIs return the US
// multi-region with inconsistent casing across services (BigQuery: "US",
// Cloud Storage: "US"), which must collapse to one filterable value.
func TestNormalizeGCPLocation(t *testing.T) {
	tests := []struct {
		name     string
		location string
		want     string
	}{
		{"uppercase multi-region US", "US", "us"},
		{"uppercase multi-region EU", "EU", "eu"},
		{"mixed-case multi-region", "Asia", "asia"},
		{"regional already lowercase", "us-central1", "us-central1"},
		{"dual-region already lowercase", "nam4", "nam4"},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, normalizeGCPLocation(tt.location))
		})
	}
}
