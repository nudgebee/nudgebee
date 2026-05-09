package providers

import (
	"encoding/json"
	"testing"
)

// TestServiceApplicationId_MarshalJSON verifies that ServiceApplicationId serializes as an object
func TestServiceApplicationId_MarshalJSON(t *testing.T) {
	id := ServiceApplicationId{
		Name:      "app/Demo-Frontend-ALB/9ef0c75b824fa80c",
		Kind:      "elbv2",
		Namespace: "us-east-1",
	}

	// Marshal to JSON
	data, err := json.Marshal(id)
	if err != nil {
		t.Fatalf("Failed to marshal ServiceApplicationId: %v", err)
	}

	// Should be an object (K8s format for downstream IDs)
	var result map[string]interface{}
	err = json.Unmarshal(data, &result)
	if err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	if result["name"] != "app/Demo-Frontend-ALB/9ef0c75b824fa80c" {
		t.Errorf("Expected name 'app/Demo-Frontend-ALB/9ef0c75b824fa80c', got %v", result["name"])
	}
	if result["kind"] != "elbv2" {
		t.Errorf("Expected kind 'elbv2', got %v", result["kind"])
	}
	if result["namespace"] != "us-east-1" {
		t.Errorf("Expected namespace 'us-east-1', got %v", result["namespace"])
	}
}

// TestServiceApplicationId_UnmarshalJSON verifies that ServiceApplicationId deserializes from an object
func TestServiceApplicationId_UnmarshalJSON(t *testing.T) {
	jsonStr := `{"name":"app/Demo-Frontend-ALB/9ef0c75b824fa80c","kind":"elbv2","namespace":"us-east-1"}`

	var id ServiceApplicationId
	err := json.Unmarshal([]byte(jsonStr), &id)
	if err != nil {
		t.Fatalf("Failed to unmarshal ServiceApplicationId: %v", err)
	}

	// Verify fields
	if id.Name != "app/Demo-Frontend-ALB/9ef0c75b824fa80c" {
		t.Errorf("Expected Name 'app/Demo-Frontend-ALB/9ef0c75b824fa80c', got '%s'", id.Name)
	}
	if id.Kind != "elbv2" {
		t.Errorf("Expected Kind 'elbv2', got '%s'", id.Kind)
	}
	if id.Namespace != "us-east-1" {
		t.Errorf("Expected Namespace 'us-east-1', got '%s'", id.Namespace)
	}
}

// TestServiceMapApplication_MarshalJSON verifies the full ServiceMapApplication structure
func TestServiceMapApplication_MarshalJSON(t *testing.T) {
	app := ServiceMapApplication{
		Id: ServiceApplicationId{
			Name:      "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/Demo-Frontend-ALB/9ef0c75b824fa80c",
			Kind:      "elbv2",
			Namespace: "us-east-1",
		},
		Upstreams: []UpstreamLink{
			{
				Id:           "Internet:external-ip:internet",
				Protocol:     "tcp",
				RequestCount: 100,
			},
		},
		Downstreams: []DownstreamLink{
			{
				Id: ServiceApplicationId{
					Name:      "sg-0d5b1e4f29f4cbb05",
					Kind:      "ec2",
					Namespace: "us-east-1",
				},
			},
		},
		Status: "active",
	}

	// Marshal to JSON
	data, err := json.Marshal(app)
	if err != nil {
		t.Fatalf("Failed to marshal ServiceMapApplication: %v", err)
	}

	// Parse back to verify structure
	var result map[string]interface{}
	err = json.Unmarshal(data, &result)
	if err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	// Verify Id is an object (ServiceApplicationId marshals as object)
	idField, ok := result["Id"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected Id to be an object, got %T: %v", result["Id"], result["Id"])
	}

	expectedName := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/Demo-Frontend-ALB/9ef0c75b824fa80c"
	if idField["name"] != expectedName {
		t.Errorf("Expected Id.name %s, got %s", expectedName, idField["name"])
	}
	if idField["kind"] != "elbv2" {
		t.Errorf("Expected Id.kind elbv2, got %s", idField["kind"])
	}
	if idField["namespace"] != "us-east-1" {
		t.Errorf("Expected Id.namespace us-east-1, got %s", idField["namespace"])
	}

	// Verify Upstreams[0].Id is a string (K8s format)
	upstreams := result["Upstreams"].([]interface{})
	if len(upstreams) > 0 {
		upstream := upstreams[0].(map[string]interface{})
		upstreamId, ok := upstream["Id"].(string)
		if !ok {
			t.Fatalf("Expected Upstreams[0].Id to be a string, got %T: %v", upstream["Id"], upstream["Id"])
		}
		expectedUpstreamId := "Internet:external-ip:internet"
		if upstreamId != expectedUpstreamId {
			t.Errorf("Expected Upstreams[0].Id %s, got %s", expectedUpstreamId, upstreamId)
		}
	}

	// Verify Downstreams[0].Id is an object (K8s format)
	downstreams := result["Downstreams"].([]interface{})
	if len(downstreams) > 0 {
		downstream := downstreams[0].(map[string]interface{})
		downstreamId, ok := downstream["Id"].(map[string]interface{})
		if !ok {
			t.Fatalf("Expected Downstreams[0].Id to be an object, got %T: %v", downstream["Id"], downstream["Id"])
		}
		if downstreamId["name"] != "sg-0d5b1e4f29f4cbb05" {
			t.Errorf("Expected Downstreams[0].Id.name sg-0d5b1e4f29f4cbb05, got %v", downstreamId["name"])
		}
		if downstreamId["kind"] != "ec2" {
			t.Errorf("Expected Downstreams[0].Id.kind ec2, got %v", downstreamId["kind"])
		}
		if downstreamId["namespace"] != "us-east-1" {
			t.Errorf("Expected Downstreams[0].Id.namespace us-east-1, got %v", downstreamId["namespace"])
		}
	}

	t.Logf("Marshaled ServiceMapApplication: %s", string(data))
}

// TestServiceApplicationId_RoundTrip verifies marshal/unmarshal round trip
func TestServiceApplicationId_RoundTrip(t *testing.T) {
	original := ServiceApplicationId{
		Name:      "test-resource",
		Kind:      "ec2",
		Namespace: "us-west-2",
	}

	// Marshal
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	// Unmarshal
	var result ServiceApplicationId
	err = json.Unmarshal(data, &result)
	if err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Verify
	if result.Name != original.Name || result.Kind != original.Kind || result.Namespace != original.Namespace {
		t.Errorf("Round trip failed: original=%+v, result=%+v", original, result)
	}
}
