package gcloud

import (
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/run/apiv2/runpb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func sampleRevision(name, image, mem string, minInst int32, envVal string) *runpb.Revision {
	return &runpb.Revision{
		Name:           "projects/p/locations/us-central1/services/svc/revisions/" + name,
		CreateTime:     timestamppb.New(time.Unix(1700000000, 0)),
		Creator:        "deployer@p.iam.gserviceaccount.com",
		ServiceAccount: "runtime@p.iam.gserviceaccount.com",
		Scaling:        &runpb.RevisionScaling{MinInstanceCount: minInst, MaxInstanceCount: 10},
		Timeout:        durationpb.New(300 * time.Second),
		// Status / managed fields that MUST be stripped from the diff view.
		ObservedGeneration: 7,
		Reconciling:        true,
		Conditions:         []*runpb.Condition{{Type: "Ready"}},
		Containers: []*runpb.Container{{
			Image:     image,
			Resources: &runpb.ResourceRequirements{Limits: map[string]string{"cpu": "1", "memory": mem}},
			Env: []*runpb.EnvVar{
				{Name: "LOG_LEVEL", Values: &runpb.EnvVar_Value{Value: envVal}},
				{Name: "DB_PASSWORD", Values: &runpb.EnvVar_ValueSource{ValueSource: &runpb.EnvVarSource{}}},
			},
		}},
	}
}

func TestRevisionToSpecYAML_NormalizedAndStatusStripped(t *testing.T) {
	yamlStr, err := revisionToSpecYAML(sampleRevision("rev-1", "gcr.io/p/app:v1", "512Mi", 1, "info"))
	if err != nil {
		t.Fatalf("revisionToSpecYAML: %v", err)
	}

	mustContain := []string{
		"image: gcr.io/p/app:v1",
		"memory: 512Mi",
		"cpu:",
		"minInstances: 1",
		"maxInstances: 10",
		"timeoutSeconds: 300",
		"serviceAccount: runtime@p.iam.gserviceaccount.com",
		"LOG_LEVEL: info",
		"DB_PASSWORD: " + secretEnvPlaceholder, // secret value never leaked
	}
	for _, s := range mustContain {
		if !strings.Contains(yamlStr, s) {
			t.Errorf("expected YAML to contain %q\n--- got ---\n%s", s, yamlStr)
		}
	}

	// Status / managed fields must NOT leak into the diff view (they churn every deploy).
	mustNotContain := []string{"observedGeneration", "reconciling", "conditions", "Ready", "uid", "etag"}
	for _, s := range mustNotContain {
		if strings.Contains(yamlStr, s) {
			t.Errorf("status/managed field %q leaked into spec YAML:\n%s", s, yamlStr)
		}
	}
}

func TestRevisionToSpecYAML_DiffIsolatesRealChange(t *testing.T) {
	oldYAML, _ := revisionToSpecYAML(sampleRevision("rev-1", "gcr.io/p/app:v1", "512Mi", 1, "info"))
	// Only the image tag changed between the two deploys.
	newYAML, _ := revisionToSpecYAML(sampleRevision("rev-2", "gcr.io/p/app:v2", "512Mi", 1, "info"))

	if oldYAML == newYAML {
		t.Fatal("expected differing YAML for differing image")
	}
	if !strings.Contains(oldYAML, "app:v1") || !strings.Contains(newYAML, "app:v2") {
		t.Errorf("image change not reflected: old has v1=%v new has v2=%v",
			strings.Contains(oldYAML, "app:v1"), strings.Contains(newYAML, "app:v2"))
	}

	// Deterministic: same revision serializes identically (stable diff, no map churn).
	again, _ := revisionToSpecYAML(sampleRevision("rev-1", "gcr.io/p/app:v1", "512Mi", 1, "info"))
	if oldYAML != again {
		t.Errorf("serialization not deterministic:\n--- a ---\n%s\n--- b ---\n%s", oldYAML, again)
	}
}

func TestShortRevisionName(t *testing.T) {
	cases := map[string]string{
		"projects/p/locations/us-central1/services/svc/revisions/svc-00042-abc": "svc-00042-abc",
		"svc-00042-abc": "svc-00042-abc",
		"":              "",
	}
	for in, want := range cases {
		if got := shortRevisionName(in); got != want {
			t.Errorf("shortRevisionName(%q) = %q, want %q", in, got, want)
		}
	}
}
