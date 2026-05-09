package traces

import "testing"

// BenchmarkExtractWorkloadFromPodName locks in the pkg-level regex optimization.
// Pre-optimization (inline regexp.MustCompile per call): ~12.7µs, 14KB, 120 allocs/op.
// Post-optimization (package-level compiled regexes):     ~1.0µs,   25B,   0 allocs/op.
func BenchmarkExtractWorkloadFromPodName(b *testing.B) {
	pods := []string{
		"nginx-deployment-7b7d7f9f9d-abcde",
		"redis-statefulset-0",
		"backup-job-12345",
		"cleanup-cronjob-1640995200-abcde",
		"simple-pod",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = extractWorkloadFromPodName(pods[i%len(pods)])
	}
}
