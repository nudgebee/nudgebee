package cloud

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFlowLogResourceFilter(t *testing.T) {
	enis := []string{"eni-0123456789abcdef0"}
	ips := []string{"192.0.2.10", "192.0.2.11"}

	// Default and explicit formats that record interface-id filter on it.
	assert.Equal(t, `filter interface_id in ["eni-0123456789abcdef0"]`, flowLogResourceFilter("", enis, ips))
	assert.Equal(t, `filter interface_id in ["eni-0123456789abcdef0"]`,
		flowLogResourceFilter("${version} ${interface-id} ${srcaddr} ${dstaddr} ${action}", enis, ips))

	// A format without interface-id falls back to the addresses, both directions.
	assert.Equal(t, `filter srcaddr in ["192.0.2.10", "192.0.2.11"] or dstaddr in ["192.0.2.10", "192.0.2.11"]`,
		flowLogResourceFilter("${version} ${srcaddr} ${dstaddr} ${action}", enis, ips))
	assert.Equal(t, `filter srcaddr in ["192.0.2.10"] or dstaddr in ["192.0.2.10"]`,
		flowLogResourceFilter("", nil, []string{"192.0.2.10"}))

	// Nothing to scope by: no filter, the query stays VPC-wide.
	assert.Empty(t, flowLogResourceFilter("", nil, nil))
	assert.Empty(t, flowLogResourceFilter("${version} ${srcaddr} ${dstaddr}", enis, nil))
}

func TestExtractInstanceNetworkScope(t *testing.T) {
	metas := []string{
		// The row with the interface list.
		`{"InstanceId":"i-0abc1234","PrivateIpAddress":"192.0.2.10","NetworkInterfaces":[{"NetworkInterfaceId":"eni-0123456789abcdef0","PrivateIpAddress":"192.0.2.10","PrivateIpAddresses":[{"PrivateIpAddress":"192.0.2.10"},{"PrivateIpAddress":"192.0.2.11"}]}]}`,
		// Another row for the same instance, with less in it.
		`{"InstanceId":"i-0abc1234","PrivateIpAddress":"192.0.2.10"}`,
		// Malformed values are dropped, not passed into the query.
		`{"NetworkInterfaceId":"eni-XYZ\" or 1=1","PrivateIpAddress":"999.1.1.1"}`,
	}
	enis, ips := extractInstanceNetworkScope(metas)
	assert.Equal(t, []string{"eni-0123456789abcdef0"}, enis)
	assert.Equal(t, []string{"192.0.2.10", "192.0.2.11"}, ips)

	enis, ips = extractInstanceNetworkScope(nil)
	assert.Empty(t, enis)
	assert.Empty(t, ips)
}

func TestGetNetworkScopeForInstanceOnlyLooksUpInstances(t *testing.T) {
	// Non-instance ids (RDS, load balancers, ARNs) are not looked up: their meta
	// has no interfaces of their own. Returns before touching the database.
	for _, id := range []string{"", "orders-db", "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/x/1", "i-zz"} {
		enis, ips := getNetworkScopeForInstance(id, "00000000-0000-0000-0000-000000000000")
		assert.Empty(t, enis, id)
		assert.Empty(t, ips, id)
	}
}
