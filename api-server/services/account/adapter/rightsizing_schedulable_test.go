package adapter

import (
	"strings"
	"testing"
)

// The cluster these tests describe is the dev one the guard was built against:
// four nodes at ~14.4Gi allocatable, and a prometheus StatefulSet whose
// container holds a 2Gi request on ip-172-31-33-159. The committed figures are
// the measured ones, and they reconcile with the noisy_neighbours evidence
// collected independently for the same node.
func devCluster() (map[string]*nodeMemory, []containerRequest) {
	nodes := map[string]*nodeMemory{
		"node-a": {name: "node-a", allocatable: 14.45 * GiB},
		"node-b": {name: "node-b", allocatable: 14.45 * GiB},
		"node-c": {name: "node-c", allocatable: 14.28 * GiB},
		"node-d": {name: "node-d", allocatable: 14.45 * GiB},
	}
	requests := []containerRequest{
		// The workload under test: 2Gi prometheus + a small sidecar, on node-a.
		{node: "node-a", namespace: "nudgebee-agent", workload: "prometheus", container: "prometheus", bytes: 2 * GiB},
		{node: "node-a", namespace: "nudgebee-agent", workload: "prometheus", container: "config-reloader", bytes: 16 * MiB},
		// Everything else already promised on each node.
		{node: "node-a", namespace: "other", workload: "other-a", container: "app", bytes: 9.00 * GiB},
		{node: "node-b", namespace: "other", workload: "other-b", container: "app", bytes: 9.10 * GiB},
		{node: "node-c", namespace: "other", workload: "other-c", container: "app", bytes: 11.19 * GiB},
		{node: "node-d", namespace: "other", workload: "other-d", container: "app", bytes: 13.14 * GiB},
	}
	return nodes, requests
}

// The value the stored recommendation actually carries today. Nothing in the
// cluster can take it, so the apply must be refused rather than leaving the
// workload Pending with its old pod already gone.
func TestCheckMemoryHeadroom_RefusesValueThatFitsNowhere(t *testing.T) {
	nodes, requests := devCluster()
	err, known := checkMemoryHeadroom(nodes, requests, "nudgebee-agent", "prometheus",
		map[string]float64{"prometheus": 10 * GiB})
	if !known {
		t.Fatal("expected a verdict, got unknown")
	}
	if err == nil {
		t.Fatal("expected 10Gi to be refused on a cluster whose largest free block is ~5.3Gi")
	}
	// The message has to carry the numbers someone needs to act, not just a refusal.
	// node-a is the roomiest option precisely because the pod's own 2Gi comes back
	// when it is replaced there — 5.4Gi, against node-b's 5.35Gi.
	for _, want := range []string{"prometheus", "no node in this cluster has room", "node-a", "5.4Gi"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal should mention %q, got: %v", want, err)
		}
	}
}

// The pod's own reservation is released when it is replaced, so on the node it
// already occupies that memory is available to the new spec. 5Gi fits there
// (3.43Gi free + 2Gi handed back) and must not be refused.
func TestCheckMemoryHeadroom_CountsBackTheWorkloadsOwnReservation(t *testing.T) {
	nodes, requests := devCluster()
	err, known := checkMemoryHeadroom(nodes, requests, "nudgebee-agent", "prometheus",
		map[string]float64{"prometheus": 5 * GiB})
	if !known {
		t.Fatal("expected a verdict, got unknown")
	}
	if err != nil {
		t.Fatalf("5Gi fits once the pod's own 2Gi is returned; should not be refused: %v", err)
	}
}

// The guard must not refuse a value merely because the pod's current node is
// full — the replacement goes through the scheduler against every node. Here
// node-a has no room but node-b does.
func TestCheckMemoryHeadroom_AllowsValueThatFitsAnotherNode(t *testing.T) {
	nodes := map[string]*nodeMemory{
		"node-a": {name: "node-a", allocatable: 8 * GiB},
		"node-b": {name: "node-b", allocatable: 64 * GiB},
	}
	requests := []containerRequest{
		{node: "node-a", namespace: "ns", workload: "app", container: "app", bytes: 1 * GiB},
		{node: "node-a", namespace: "other", workload: "filler", container: "app", bytes: 6 * GiB},
	}
	err, known := checkMemoryHeadroom(nodes, requests, "ns", "app",
		map[string]float64{"app": 16 * GiB})
	if !known {
		t.Fatal("expected a verdict, got unknown")
	}
	if err != nil {
		t.Fatalf("16Gi does not fit node-a but fits node-b; must not be refused: %v", err)
	}
}

// A pod is scheduled as a whole, so an unchanged sidecar still counts toward
// what has to fit. 5Gi for the main container plus a 10Gi sidecar fits nowhere
// even though neither value alone is impossible.
func TestCheckMemoryHeadroom_TotalsEveryContainerInThePod(t *testing.T) {
	nodes := map[string]*nodeMemory{
		"node-a": {name: "node-a", allocatable: 14 * GiB},
	}
	requests := []containerRequest{
		{node: "node-a", namespace: "ns", workload: "app", container: "app", bytes: 1 * GiB},
		{node: "node-a", namespace: "ns", workload: "app", container: "sidecar", bytes: 10 * GiB},
	}
	err, known := checkMemoryHeadroom(nodes, requests, "ns", "app",
		map[string]float64{"app": 5 * GiB})
	if !known {
		t.Fatal("expected a verdict, got unknown")
	}
	if err == nil {
		t.Fatal("5Gi + the pod's unchanged 10Gi sidecar exceeds a 14Gi node; expected a refusal")
	}
}

// A workload the inventory has never seen gives us no way to total the new pod
// or to hand its own reservation back, so a value that is above every node's
// free memory but below a node's size must read as unknown and let the apply
// through rather than refusing on data it does not have. 10Gi is that value
// here: more than the 5.35Gi free anywhere, less than a 14.45Gi node.
func TestCheckMemoryHeadroom_UnknownWhenWorkloadNotInInventory(t *testing.T) {
	nodes, requests := devCluster()
	err, known := checkMemoryHeadroom(nodes, requests, "nudgebee-agent", "not-collected-yet",
		map[string]float64{"app": 10 * GiB})
	if known {
		t.Fatal("a workload absent from the pod inventory must read as unknown, not as a verdict")
	}
	if err != nil {
		t.Fatalf("an unknown workload below node size must not be refused: %v", err)
	}
}

// A workload with no placed pod in the inventory, asked for 60000Mi on a
// cluster whose nodes hold 12345Mi each. No inventory is needed to know that
// cannot schedule, so it is refused on node size alone — with the pod inventory
// empty, as it is when collection is behind.
func TestCheckMemoryHeadroom_RefusesValueLargerThanAnyNodeWithoutItsPods(t *testing.T) {
	nodes := map[string]*nodeMemory{
		"node-b": {name: "node-b", allocatable: 12345 * MiB},
		"node-a": {name: "node-a", allocatable: 12345 * MiB},
		"node-c": {name: "node-c", allocatable: 8000 * MiB},
	}
	err, known := checkMemoryHeadroom(nodes, nil, "shop", "checkout",
		map[string]float64{"app": 60000 * MiB})
	if !known {
		t.Fatal("a request larger than every node is a verdict even without the workload's pods")
	}
	if err == nil {
		t.Fatal("expected 60000Mi to be refused on a cluster whose largest node holds 12345Mi")
	}
	// The container, what it asked for, and the largest node — the first by name
	// of the two that tie, so the message does not change between attempts.
	// ... and the size it would have to come down to, in the whole Mi the forms take.
	for _, want := range []string{"app needs 58.6Gi", "no node in this cluster is that large", "node-a", "12.1Gi", "has to be 12345Mi or less"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal should mention %q, got: %v", want, err)
		}
	}
}

// Other pods' reservations must not turn the node-size check into a free-memory
// one. With the workload's own pods unseen we cannot tell which reservations
// are its own, so a full cluster is no reason to refuse a value a node could
// hold: 10Gi on 12Gi nodes that are 11Gi committed stays unknown.
func TestCheckMemoryHeadroom_NodeSizeCheckIgnoresWhatOtherPodsReserve(t *testing.T) {
	nodes := map[string]*nodeMemory{
		"node-a": {name: "node-a", allocatable: 12 * GiB},
	}
	requests := []containerRequest{
		{node: "node-a", namespace: "other", workload: "filler", container: "app", bytes: 11 * GiB},
	}
	err, known := checkMemoryHeadroom(nodes, requests, "ns", "unseen",
		map[string]float64{"app": 10 * GiB})
	if known || err != nil {
		t.Fatalf("10Gi fits a 12Gi node by size; without the workload's pods that must stay unknown, got known=%v err=%v", known, err)
	}
}

// Several containers changed at once are one pod, so they are judged together:
// 8Gi + 8Gi cannot share a 12Gi node even though each alone could.
func TestCheckMemoryHeadroom_NodeSizeCheckTotalsTheChangedContainers(t *testing.T) {
	nodes := map[string]*nodeMemory{
		"node-a": {name: "node-a", allocatable: 12 * GiB},
	}
	err, known := checkMemoryHeadroom(nodes, nil, "ns", "unseen",
		map[string]float64{"app": 8 * GiB, "sidecar": 8 * GiB})
	if !known || err == nil {
		t.Fatalf("16Gi across two containers exceeds a 12Gi node; expected a refusal, got known=%v err=%v", known, err)
	}
	for _, want := range []string{"unseen needs 16.0Gi", "Together the requests have to be 12288Mi or less"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("with more than one container changed the refusal names the workload and the total; want %q, got: %v", want, err)
		}
	}
}

// Advice that is refused when followed is worse than none, so the size a
// refusal quotes has to be one the check then accepts. On node-a the free block
// is 5.45Gi and the unchanged sidecar takes 16Mi of it, which leaves the
// prometheus container 5564Mi — not the 5.4Gi the rounded block size suggests,
// and not the whole block.
func TestCheckMemoryHeadroom_QuotedSizeIsOneThatPasses(t *testing.T) {
	nodes, requests := devCluster()
	err, _ := checkMemoryHeadroom(nodes, requests, "nudgebee-agent", "prometheus",
		map[string]float64{"prometheus": 10 * GiB})
	if err == nil || !strings.Contains(err.Error(), "Lower the request to 5564Mi or less") {
		t.Fatalf("expected the refusal to quote 5564Mi, got: %v", err)
	}

	nodes, requests = devCluster()
	if err, _ := checkMemoryHeadroom(nodes, requests, "nudgebee-agent", "prometheus",
		map[string]float64{"prometheus": 5564 * MiB}); err != nil {
		t.Fatalf("the quoted 5564Mi must be accepted, got: %v", err)
	}
	nodes, requests = devCluster()
	if err, _ := checkMemoryHeadroom(nodes, requests, "nudgebee-agent", "prometheus",
		map[string]float64{"prometheus": 5565 * MiB}); err == nil {
		t.Fatal("5565Mi is past what fits and must still be refused")
	}
}

// Several containers changed at once share one figure: what they may ask for
// together.
func TestCheckMemoryHeadroom_QuotedSizeCoversEveryChangedContainer(t *testing.T) {
	nodes := map[string]*nodeMemory{
		"node-a": {name: "node-a", allocatable: 14 * GiB},
	}
	requests := []containerRequest{
		{node: "node-a", namespace: "ns", workload: "app", container: "app", bytes: 1 * GiB},
		{node: "node-a", namespace: "ns", workload: "app", container: "sidecar", bytes: 1 * GiB},
	}
	err, _ := checkMemoryHeadroom(nodes, requests, "ns", "app",
		map[string]float64{"app": 8 * GiB, "sidecar": 8 * GiB})
	if err == nil || !strings.Contains(err.Error(), "Lower the requests to 14336Mi or less in total") {
		t.Fatalf("expected one total for both containers, got: %v", err)
	}
}

// When the pod's unchanged containers already take everything the roomiest node
// can give, no lower value for the changed one helps, and the refusal must not
// invent one.
func TestCheckMemoryHeadroom_NoSizeQuotedWhenNothingFits(t *testing.T) {
	nodes := map[string]*nodeMemory{
		"node-a": {name: "node-a", allocatable: 4 * GiB},
	}
	requests := []containerRequest{
		{node: "node-a", namespace: "ns", workload: "app", container: "app", bytes: 0},
		{node: "node-a", namespace: "ns", workload: "app", container: "sidecar", bytes: 1 * GiB},
		{node: "node-a", namespace: "other", workload: "filler", container: "app", bytes: 3 * GiB},
	}
	err, _ := checkMemoryHeadroom(nodes, requests, "ns", "app",
		map[string]float64{"app": 2 * GiB})
	if err == nil {
		t.Fatal("2Gi on a full node must be refused")
	}
	if strings.Contains(err.Error(), "Lower the request") || !strings.HasSuffix(err.Error(), "Add capacity") {
		t.Errorf("with no room left for the container the refusal should only say to add capacity, got: %v", err)
	}
}

// A node whose allocatable is unknown may be the one node large enough, so the
// rest of the list cannot stand in for the cluster: the whole inventory reads as
// empty, and the caller skips the check.
func TestNodeMemoryFromRows_UnknownNodeSizeMakesTheInventoryUnusable(t *testing.T) {
	got := nodeMemoryFromRows([]nodeRow{
		{Name: "node-a", MemoryAllocatable: 12345},
		{Name: "node-b", MemoryAllocatable: 0},
	})
	if len(got) != 0 {
		t.Fatalf("a node of unknown size must leave no usable inventory, got %d nodes", len(got))
	}
	got = nodeMemoryFromRows([]nodeRow{
		{Name: "node-a", MemoryAllocatable: 12345},
		{Name: "node-b", MemoryAllocatable: 26500},
	})
	// k8s_nodes stores MiB; the check works in bytes.
	if len(got) != 2 || got["node-b"].allocatable != 26500*MiB {
		t.Fatalf("a complete inventory must come through whole and in bytes, got %v", got)
	}
}

// Stale or partial inventory must never turn into a refusal. loadNodeMemory
// returns no nodes when any node's allocatable is unknown, so the decision can
// be handed no nodes at all — which has to read as unknown, not as "nothing fits".
func TestCheckMemoryHeadroom_EmptyNodeInventoryIsUnknown(t *testing.T) {
	err, known := checkMemoryHeadroom(map[string]*nodeMemory{}, nil, "ns", "app",
		map[string]float64{"app": 1 * GiB})
	if known {
		t.Fatal("an empty node inventory must read as unknown, not as a verdict")
	}
	if err != nil {
		t.Fatalf("an empty node inventory must not produce a refusal: %v", err)
	}
}

func TestRequestedMemoryByContainer(t *testing.T) {
	got := requestedMemoryByContainer([]any{
		map[string]any{"container_name": "app", "memory_request": "2Gi"},
		map[string]any{"container_name": "sidecar", "memory_request": "256Mi"},
		// No name, and a CPU-style quantity: both ignored rather than guessed at.
		map[string]any{"memory_request": "1Gi"},
		map[string]any{"container_name": "bad", "memory_request": "91m"},
	})
	if len(got) != 2 {
		t.Fatalf("expected 2 parsed containers, got %d (%v)", len(got), got)
	}
	if got["app"] != 2*GiB {
		t.Errorf("app: want %v, got %v", float64(2*GiB), got["app"])
	}
	if got["sidecar"] != 256*MiB {
		t.Errorf("sidecar: want %v, got %v", float64(256*MiB), got["sidecar"])
	}
}
