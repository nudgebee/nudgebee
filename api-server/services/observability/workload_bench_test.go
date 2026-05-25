package observability

import "testing"

// BenchmarkExtractWorkloadFromPodName measures the performance of pod-name
// to workload-name extraction. The previous implementation used hand-rolled
// splitPodName (O(n²) via string += per char) and joinParts (O(n²) via
// string += per segment). The optimized version uses strings.FieldsFunc
// and strings.Join for O(n) behavior with far fewer allocations.
func BenchmarkExtractWorkloadFromPodName(b *testing.B) {
	pods := []string{
		"nginx-deployment-7b7d7f9f9d-abcde",
		"redis-cache-abc12-xyz98",
		"my-app-frontend-server-a1b2c-d3e4f",
		"simple-pod",
		"a",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = extractWorkloadFromPodName(pods[i%len(pods)])
	}
}
