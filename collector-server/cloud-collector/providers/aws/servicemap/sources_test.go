package servicemap

import (
	"nudgebee/collector/cloud/providers"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Tests for mergeUpstreamLinks
func TestMergeUpstreamLinks_NoDuplicates(t *testing.T) {
	linksA := []providers.UpstreamLink{
		{Id: "service-a:ec2:us-east-1", RequestCount: 100, Status: 200},
	}
	linksB := []providers.UpstreamLink{
		{Id: "service-b:rds:us-east-1", RequestCount: 50, Status: 200},
	}
	merged := mergeUpstreamLinks(linksA, linksB)
	assert.Equal(t, 2, len(merged), "Should have 2 unique upstream links")
}

func TestMergeUpstreamLinks_WithDuplicates_AggregatesMetrics(t *testing.T) {
	linksA := []providers.UpstreamLink{
		{Id: "Internet:external-ip:internet", RequestCount: 0, FailureCount: 0, Status: 0, Protocol: ""},
	}
	linksB := []providers.UpstreamLink{
		{Id: "Internet:external-ip:internet", RequestCount: 999, FailureCount: 16, Status: 500, Protocol: "TCP", BytesSent: 1024000, BytesReceived: 512000},
	}
	merged := mergeUpstreamLinks(linksA, linksB)
	assert.Equal(t, 1, len(merged), "Should deduplicate to 1 upstream link")
	link := merged[0]
	assert.Equal(t, "Internet:external-ip:internet", link.Id)
	assert.Equal(t, float64(999), link.RequestCount)
	assert.Equal(t, float64(16), link.FailureCount)
	assert.Equal(t, 500, link.Status)
	assert.Equal(t, "TCP", link.Protocol)
	assert.Equal(t, float64(1024000), link.BytesSent)
	assert.Equal(t, float64(512000), link.BytesReceived)
}

// Tests for mergeDownstreamLinks
func TestMergeDownstreamLinks_NoDuplicates(t *testing.T) {
	linksA := []providers.DownstreamLink{
		{Id: providers.ServiceApplicationId{Name: "service-a", Kind: "ec2", Namespace: "us-east-1"}, RequestCount: 100, Status: 200},
	}
	linksB := []providers.DownstreamLink{
		{Id: providers.ServiceApplicationId{Name: "service-b", Kind: "rds", Namespace: "us-east-1"}, RequestCount: 50, Status: 200},
	}
	merged := mergeDownstreamLinks(linksA, linksB)
	assert.Equal(t, 2, len(merged), "Should have 2 unique downstream links")
}

func TestMergeDownstreamLinks_WithDuplicates_AggregatesMetrics(t *testing.T) {
	linksA := []providers.DownstreamLink{
		{Id: providers.ServiceApplicationId{Name: "Internet", Kind: "external-ip", Namespace: "internet"}, RequestCount: 0, FailureCount: 0, Status: 0, Protocol: ""},
	}
	linksB := []providers.DownstreamLink{
		{Id: providers.ServiceApplicationId{Name: "Internet", Kind: "external-ip", Namespace: "internet"}, RequestCount: 999, FailureCount: 16, Status: 500, Protocol: "TCP", BytesSent: 1024000, BytesReceived: 512000},
	}
	merged := mergeDownstreamLinks(linksA, linksB)
	assert.Equal(t, 1, len(merged), "Should deduplicate to 1 downstream link")
	link := merged[0]
	assert.Equal(t, "Internet", link.Id.Name)
	assert.Equal(t, float64(999), link.RequestCount)
	assert.Equal(t, float64(16), link.FailureCount)
	assert.Equal(t, 500, link.Status)
	assert.Equal(t, "TCP", link.Protocol)
	assert.Equal(t, float64(1024000), link.BytesSent)
	assert.Equal(t, float64(512000), link.BytesReceived)
}

func TestMergeDownstreamLinks_MultipleSourcesWithSameLink(t *testing.T) {
	linksA := []providers.DownstreamLink{
		{Id: providers.ServiceApplicationId{Name: "alb", Kind: "elbv2", Namespace: "us-east-1"}, RequestCount: 100, FailureCount: 5, Status: 500, Protocol: "TCP", BytesSent: 1000},
	}
	linksB := []providers.DownstreamLink{
		{Id: providers.ServiceApplicationId{Name: "alb", Kind: "elbv2", Namespace: "us-east-1"}, RequestCount: 200, FailureCount: 10, Status: 200, Protocol: "TCP", BytesReceived: 2000},
	}
	merged := mergeDownstreamLinks(linksA, linksB)
	assert.Equal(t, 1, len(merged))
	link := merged[0]
	assert.Equal(t, float64(300), link.RequestCount)
	assert.Equal(t, float64(15), link.FailureCount)
	assert.Equal(t, 500, link.Status)
	assert.Equal(t, float64(1000), link.BytesSent)
	assert.Equal(t, float64(2000), link.BytesReceived)
}

func TestMergeDownstreamLinks_LatencyAveraging(t *testing.T) {
	linksA := []providers.DownstreamLink{
		{Id: providers.ServiceApplicationId{Name: "svc", Kind: "rds", Namespace: "us-east-1"}, Latency: 10.0},
	}
	linksB := []providers.DownstreamLink{
		{Id: providers.ServiceApplicationId{Name: "svc", Kind: "rds", Namespace: "us-east-1"}, Latency: 20.0},
	}
	merged := mergeDownstreamLinks(linksA, linksB)
	assert.Equal(t, 1, len(merged))
	assert.Equal(t, 15.0, merged[0].Latency)
}
