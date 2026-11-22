package event

import (
	"testing"

	integrationcore "nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
)

func TestSetVMSubjectFields(t *testing.T) {
	vm := integrationcore.VMSubject{CloudResourceID: "res-1", Name: "web-1"}

	tests := []struct {
		name      string
		in        Event
		wantOwner string
		wantKind  string
		wantName  string
		wantType  string
		wantRes   string
	}{
		{
			name:      "host alert owns the VM",
			in:        Event{SubjectType: "instance", SubjectName: "10.0.0.5:9100", Labels: map[string]string{"alertname": "HostDiskSpaceCritical"}},
			wantOwner: "web-1", wantKind: "vm", wantName: "web-1", wantType: "vm", wantRes: "res-1",
		},
		{
			name:      "service alert owns the unit",
			in:        Event{SubjectType: "instance", SubjectName: "10.0.0.5:9100", Labels: map[string]string{"container_id": "/system.slice/nb-bench-oom.service"}},
			wantOwner: "nb-bench-oom.service", wantKind: "service", wantName: "web-1", wantType: "vm", wantRes: "res-1",
		},
		{
			name:      "owner left as the resource id is replaced by the VM",
			in:        Event{SubjectType: "service", SubjectName: "forager", SubjectOwner: "res-1", Labels: map[string]string{}},
			wantOwner: "web-1", wantKind: "vm", wantName: "forager", wantType: "service", wantRes: "res-1",
		},
		{
			name:      "an owner already set is kept for a host alert",
			in:        Event{SubjectType: "service", SubjectName: "forager", SubjectOwner: "custom", Labels: map[string]string{}},
			wantOwner: "custom", wantKind: "", wantName: "forager", wantType: "service", wantRes: "res-1",
		},
		{
			name:      "already linked event keeps its link and subject",
			in:        Event{SubjectType: "instance", SubjectName: "old", CloudResourceId: "res-0", Labels: map[string]string{}},
			wantOwner: "web-1", wantKind: "vm", wantName: "old", wantType: "instance", wantRes: "res-0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := tt.in
			setVMSubjectFields(&e, vm)
			assert.Equal(t, tt.wantOwner, e.SubjectOwner)
			assert.Equal(t, tt.wantKind, e.SubjectOwnerKind)
			assert.Equal(t, tt.wantName, e.SubjectName)
			assert.Equal(t, tt.wantType, e.SubjectType)
			assert.Equal(t, tt.wantRes, e.CloudResourceId)
		})
	}
}

func TestVMSubjectLookupNeeded(t *testing.T) {
	svc := map[string]string{"container_id": "/system.slice/a.service"}
	tests := []struct {
		name string
		in   Event
		want bool
	}{
		{"unlinked event", Event{}, true},
		{"linked, no owner", Event{CloudResourceId: "res-1"}, true},
		{"linked, owner is the resource id", Event{CloudResourceId: "res-1", SubjectOwner: "res-1"}, true},
		{"linked, own owner, no service", Event{CloudResourceId: "res-1", SubjectOwner: "checkout"}, false},
		{"linked, own owner, service label", Event{CloudResourceId: "res-1", SubjectOwner: "checkout", Labels: svc}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, vmSubjectLookupNeeded(&tt.in))
		})
	}
}
