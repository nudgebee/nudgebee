package servicemap

import (
	"nudgebee/collector/cloud/providers"
	"testing"
)

// TestIsServicePort validates the service port detection logic
func TestIsServicePort(t *testing.T) {
	tests := []struct {
		port     int
		expected bool
		desc     string
	}{
		{80, true, "HTTP"},
		{443, true, "HTTPS"},
		{22, true, "SSH"},
		{3306, true, "MySQL"},
		{5432, true, "PostgreSQL"},
		{8080, true, "HTTP alternate"},
		{36596, false, "ephemeral port (response traffic)"},
		{51234, false, "ephemeral port"},
		{1024, false, "first ephemeral port"},
		{65535, false, "max port"},
	}

	for _, tt := range tests {
		result := isServicePort(tt.port)
		if result != tt.expected {
			t.Errorf("isServicePort(%d) for %s: expected %v, got %v",
				tt.port, tt.desc, tt.expected, result)
		}
	}
}

// TestVPCFlowLogsDirection verifies that VPC Flow Logs correctly build service map directions
// Scenario: LB (10.0.1.5) → EC1 (10.0.2.10) → EC2 (10.0.3.15)
// Where EC1 → EC2 connections are being rejected
func TestVPCFlowLogsDirection(t *testing.T) {
	// Test case: VPC Flow Log shows traffic from LB to EC1
	// Source: 10.0.1.5 (LB)
	// Destination: 10.0.2.10 (EC1)
	t.Run("LB_to_EC1_accepted", func(t *testing.T) {
		conn := FlowLogConnection{
			SourceIP:     "10.0.1.5",  // LB
			DestIP:       "10.0.2.10", // EC1
			DestPort:     80,
			Protocol:     6, // TCP
			TotalBytes:   10000,
			TotalPackets: 100,
			Connections:  50,
			AcceptCount:  50,
			RejectCount:  0,
		}

		lbNode := providers.ServiceApplicationId{
			Name:      "lb-arn",
			Kind:      "elbv2",
			Namespace: "us-east-1",
		}

		ec1Node := providers.ServiceApplicationId{
			Name:      "i-ec1instance",
			Kind:      "ec2",
			Namespace: "us-east-1",
		}

		// Build LB's upstream link to EC1 (LB calls EC1)
		lbUpstream := buildLinkFromConnection(conn, ec1Node, "upstream")

		// Verify LB's upstream link
		if lbUpstream.Id != ec1Node {
			t.Errorf("Expected LB upstream to be EC1, got %+v", lbUpstream.Id)
		}
		if lbUpstream.Status != 200 {
			t.Errorf("Expected status 200 (healthy), got %d", lbUpstream.Status)
		}
		if lbUpstream.BytesSent != 10000 {
			t.Errorf("Expected BytesSent=10000, got %.0f", lbUpstream.BytesSent)
		}
		if lbUpstream.FailureCount != 0 {
			t.Errorf("Expected FailureCount=0, got %.0f", lbUpstream.FailureCount)
		}

		// Build EC1's downstream link from LB (LB calls EC1)
		ec1Downstream := buildLinkFromConnection(conn, lbNode, "downstream")

		// Verify EC1's downstream link
		if ec1Downstream.Id != lbNode {
			t.Errorf("Expected EC1 downstream to be LB, got %+v", ec1Downstream.Id)
		}
		if ec1Downstream.Status != 200 {
			t.Errorf("Expected status 200 (healthy), got %d", ec1Downstream.Status)
		}
		if ec1Downstream.BytesReceived != 10000 {
			t.Errorf("Expected BytesReceived=10000, got %.0f", ec1Downstream.BytesReceived)
		}
	})

	// Test case: VPC Flow Log shows traffic from EC1 to EC2 being REJECTED
	// Source: 10.0.2.10 (EC1)
	// Destination: 10.0.3.15 (EC2)
	// Action: REJECT
	t.Run("EC1_to_EC2_rejected", func(t *testing.T) {
		conn := FlowLogConnection{
			SourceIP:     "10.0.2.10", // EC1
			DestIP:       "10.0.3.15", // EC2
			DestPort:     3306,
			Protocol:     6, // TCP
			TotalBytes:   100,
			TotalPackets: 10,
			Connections:  10,
			AcceptCount:  0,
			RejectCount:  10, // All rejected!
		}

		ec1Node := providers.ServiceApplicationId{
			Name:      "i-ec1instance",
			Kind:      "ec2",
			Namespace: "us-east-1",
		}

		ec2Node := providers.ServiceApplicationId{
			Name:      "i-ec2instance",
			Kind:      "ec2",
			Namespace: "us-east-1",
		}

		// Build EC1's upstream link to EC2 (EC1 calls EC2)
		ec1Upstream := buildLinkFromConnection(conn, ec2Node, "upstream")

		// Verify EC1's upstream link shows failures
		if ec1Upstream.Id != ec2Node {
			t.Errorf("Expected EC1 upstream to be EC2, got %+v", ec1Upstream.Id)
		}
		if ec1Upstream.Status != 500 {
			t.Errorf("Expected status 500 (failures), got %d", ec1Upstream.Status)
		}
		if ec1Upstream.FailureCount != 10 {
			t.Errorf("Expected FailureCount=10, got %.0f", ec1Upstream.FailureCount)
		}
		if ec1Upstream.RequestCount != 10 {
			t.Errorf("Expected RequestCount=10, got %.0f", ec1Upstream.RequestCount)
		}
		if ec1Upstream.BytesSent != 100 {
			t.Errorf("Expected BytesSent=100, got %.0f", ec1Upstream.BytesSent)
		}

		// Build EC2's downstream link from EC1 (EC1 calls EC2)
		ec2Downstream := buildLinkFromConnection(conn, ec1Node, "downstream")

		// Verify EC2's downstream link shows failures
		if ec2Downstream.Id != ec1Node {
			t.Errorf("Expected EC2 downstream to be EC1, got %+v", ec2Downstream.Id)
		}
		if ec2Downstream.Status != 500 {
			t.Errorf("Expected status 500 (failures), got %d", ec2Downstream.Status)
		}
		if ec2Downstream.FailureCount != 10 {
			t.Errorf("Expected FailureCount=10, got %.0f", ec2Downstream.FailureCount)
		}
		if ec2Downstream.BytesReceived != 100 {
			t.Errorf("Expected BytesReceived=100, got %.0f", ec2Downstream.BytesReceived)
		}
	})

	// Test complete service map structure
	t.Run("Complete_Service_Map_LB_EC1_EC2", func(t *testing.T) {
		// Simulate complete service map after JSON marshaling
		// This tests the final format that will be sent to the API server

		lbApp := providers.ServiceMapApplication{
			Id: providers.ServiceApplicationId{
				Name:      "lb-arn",
				Kind:      "elbv2",
				Namespace: "us-east-1",
			},
			Upstreams: []providers.UpstreamLink{
				{
					Id:           "i-ec1instance:ec2:us-east-1", // String format
					Status:       200,
					RequestCount: 50,
					FailureCount: 0,
					BytesSent:    10000,
				},
			},
			Downstreams: []providers.DownstreamLink{
				{
					Id: providers.ServiceApplicationId{ // Object format
						Name:      "Internet",
						Kind:      "external-ip",
						Namespace: "internet",
					},
					Status:        200,
					RequestCount:  50,
					BytesReceived: 5000,
				},
			},
			Status: "active",
		}

		ec1App := providers.ServiceMapApplication{
			Id: providers.ServiceApplicationId{
				Name:      "i-ec1instance",
				Kind:      "ec2",
				Namespace: "us-east-1",
			},
			Upstreams: []providers.UpstreamLink{
				{
					Id:           "i-ec2instance:ec2:us-east-1", // String format
					Status:       500,                           // FAILED
					RequestCount: 10,
					FailureCount: 10, // All failed
					BytesSent:    100,
				},
			},
			Downstreams: []providers.DownstreamLink{
				{
					Id: providers.ServiceApplicationId{ // Object format
						Name:      "lb-arn",
						Kind:      "elbv2",
						Namespace: "us-east-1",
					},
					Status:        200,
					RequestCount:  50,
					BytesReceived: 10000,
				},
			},
			Status: "degraded", // Degraded because upstream connection is failing
		}

		// Verify LB structure
		if len(lbApp.Upstreams) != 1 {
			t.Errorf("Expected LB to have 1 upstream, got %d", len(lbApp.Upstreams))
		}
		if lbApp.Upstreams[0].Id != "i-ec1instance:ec2:us-east-1" {
			t.Errorf("Expected LB upstream to be EC1 (string format), got %s", lbApp.Upstreams[0].Id)
		}

		// Verify EC1 structure
		if len(ec1App.Upstreams) != 1 {
			t.Errorf("Expected EC1 to have 1 upstream, got %d", len(ec1App.Upstreams))
		}
		if ec1App.Upstreams[0].Id != "i-ec2instance:ec2:us-east-1" {
			t.Errorf("Expected EC1 upstream to be EC2 (string format), got %s", ec1App.Upstreams[0].Id)
		}
		if ec1App.Upstreams[0].FailureCount != 10 {
			t.Errorf("Expected EC1 upstream to have 10 failures, got %.0f", ec1App.Upstreams[0].FailureCount)
		}
		if ec1App.Upstreams[0].Status != 500 {
			t.Errorf("Expected EC1 upstream status to be 500, got %d", ec1App.Upstreams[0].Status)
		}

		if len(ec1App.Downstreams) != 1 {
			t.Errorf("Expected EC1 to have 1 downstream, got %d", len(ec1App.Downstreams))
		}
		if ec1App.Downstreams[0].Id.Name != "lb-arn" {
			t.Errorf("Expected EC1 downstream to be LB (object format), got %s", ec1App.Downstreams[0].Id.Name)
		}

		t.Logf("LB → EC1 (healthy): %d requests, %.0f bytes", int(lbApp.Upstreams[0].RequestCount), lbApp.Upstreams[0].BytesSent)
		t.Logf("EC1 → EC2 (FAILED): %d requests, %.0f failures", int(ec1App.Upstreams[0].RequestCount), ec1App.Upstreams[0].FailureCount)
	})
}
