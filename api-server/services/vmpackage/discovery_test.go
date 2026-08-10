package vmpackage

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realDiscoveryInventoryResponse is a trimmed capture of a real
// discovery_inventory response (full package lists shortened to a few
// representative lines) — pins the actual wire shape rather than an
// invented one.
const realDiscoveryInventoryResponse = `{
  "status_code": 200,
  "request_id": "1bfaa67e-bcfe-441f-b07f-d78f91c034f0",
  "action": "discovery_inventory",
  "result": {
    "content_pack_version": 2,
    "targets": [
      {
        "host": "172.31.0.11",
        "status": "ok",
        "facts": {
          "arch": "x86_64",
          "os_family": "debian",
          "os_id": "ubuntu",
          "os_major": "22"
        },
        "collectors": {
          "hostname": "ip-172-31-0-11.ec2.internal\n",
          "machine-id": "ec2403e319a2f3f0ae53a05e3daf084b\n",
          "os-release": "PRETTY_NAME=\"Ubuntu 22.04.5 LTS\"\nNAME=\"Ubuntu\"\nVERSION_ID=\"22.04\"\nID=ubuntu\nID_LIKE=debian\n",
          "pkgs-dpkg": "acpid\t1:2.0.33-1ubuntu1\tamd64\tinstalled\tacpid\t1:2.0.33-1ubuntu1\nbash\t5.1-6ubuntu1.1\tamd64\tinstalled\tbash\t5.1-6ubuntu1.1\n"
        }
      }
    ]
  }
}`

func TestParseDiscoveryInventoryResult_RealResponseShape(t *testing.T) {
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(realDiscoveryInventoryResponse), &result))

	target, err := parseDiscoveryInventoryResult(result)
	require.NoError(t, err)

	assert.Equal(t, "172.31.0.11", target.Host)
	assert.Equal(t, "ok", target.Status)
	assert.Contains(t, target.Collectors["os-release"], "VERSION_ID=\"22.04\"")
	assert.Contains(t, target.Collectors["pkgs-dpkg"], "bash\t5.1-6ubuntu1.1")

	family, version, err := ParseOSRelease(target.Collectors["os-release"])
	require.NoError(t, err)
	assert.Equal(t, "ubuntu", family)
	assert.Equal(t, "22.04", version)

	pkgs, err := parsePackages(target.Collectors)
	require.NoError(t, err)
	require.Len(t, pkgs, 2)
	assert.Equal(t, "acpid", pkgs[0].Name)
}

func TestParseDiscoveryInventoryResult_NoTargets(t *testing.T) {
	_, err := parseDiscoveryInventoryResult(map[string]any{
		"result": map[string]any{"targets": []any{}},
	})
	assert.Error(t, err)
}

func TestParseDiscoveryInventoryResult_TargetNotOK(t *testing.T) {
	_, err := parseDiscoveryInventoryResult(map[string]any{
		"result": map[string]any{
			"targets": []any{
				map[string]any{"host": "10.0.0.5", "status": "unreachable"},
			},
		},
	})
	assert.Error(t, err)
}

func TestParsePackages_NoCollector(t *testing.T) {
	_, err := parsePackages(map[string]string{"os-release": "ID=ubuntu\nVERSION_ID=22.04\n"})
	assert.Error(t, err)
}

func TestParseDiscoveryInventoryResultBatch_MultipleTargets(t *testing.T) {
	targets, err := parseDiscoveryInventoryResultBatch(map[string]any{
		"result": map[string]any{
			"targets": []any{
				map[string]any{"host": "10.0.0.5", "status": "ok"},
				map[string]any{"host": "10.0.0.6", "status": "ssh-auth-failed", "error": "auth failed"},
				map[string]any{"host": "10.0.0.7", "status": "timeout"},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, targets, 3)
	assert.Equal(t, "ok", targets[0].Status)
	assert.Equal(t, "ssh-auth-failed", targets[1].Status)
	assert.Equal(t, "timeout", targets[2].Status)
}

func TestParseDiscoveryInventoryResultBatch_NoTargets(t *testing.T) {
	_, err := parseDiscoveryInventoryResultBatch(map[string]any{
		"result": map[string]any{"targets": []any{}},
	})
	assert.Error(t, err)
}

// realDiscoverySweepResponse is shaped after forager's SweepResult
// (pkg/proxy/discovery/sweep.go) as wrapped by the relay's ActionResponse
// envelope, mirroring realDiscoveryInventoryResponse above.
const realDiscoverySweepResponse = `{
  "status_code": 200,
  "request_id": "9f3a1c2e-1111-4444-8888-abcdefabcdef",
  "action": "discovery_sweep",
  "result": {
    "cidrs": ["172.31.0.0/28"],
    "addresses_scanned": 14,
    "rate_pps": 100,
    "addresses_excluded": 0,
    "hosts": [
      {"ip": "172.31.0.11", "mac": "02:42:ac:1f:00:0b", "rdns": "", "open_ports": [22], "sources": ["tcp"]},
      {"ip": "172.31.0.12", "open_ports": [3389], "sources": ["tcp"]}
    ],
    "duration_seconds": 1.2
  }
}`

func TestDiscoverySweepResponse_RealShape(t *testing.T) {
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(realDiscoverySweepResponse), &result))

	raw, err := json.Marshal(result)
	require.NoError(t, err)
	var resp discoverySweepResponse
	require.NoError(t, json.Unmarshal(raw, &resp))

	require.Len(t, resp.Result.Hosts, 2)
	assert.Equal(t, "172.31.0.11", resp.Result.Hosts[0].IP)
	assert.Equal(t, []int{22}, resp.Result.Hosts[0].OpenPorts)
	assert.Equal(t, "172.31.0.12", resp.Result.Hosts[1].IP)
	assert.Empty(t, resp.Result.Hosts[1].MAC)
}
