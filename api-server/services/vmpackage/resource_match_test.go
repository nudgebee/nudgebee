package vmpackage

import (
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestParseCloudInstanceIdentity_AWS(t *testing.T) {
	id, ok := parseCloudInstanceIdentity("provider=aws\ninstance_id=i-0aab26d051729d673\nregion=us-east-1\npublic_ip=54.1.2.3")
	require.True(t, ok)
	assert.Equal(t, "aws", id.Provider)
	assert.Equal(t, "i-0aab26d051729d673", id.InstanceID)
	assert.Equal(t, "us-east-1", id.Region)
	assert.Equal(t, "54.1.2.3", id.PublicIP)
}

func TestParseCloudInstanceIdentity_AWS_MissingInstanceID(t *testing.T) {
	_, ok := parseCloudInstanceIdentity("provider=aws\nregion=us-east-1")
	assert.False(t, ok)
}

func TestParseCloudInstanceIdentity_GCP_ZoneToRegion(t *testing.T) {
	id, ok := parseCloudInstanceIdentity("provider=gcp\ninstance_id=1020227973377891447\nzone=us-central1-a\npublic_ip=")
	require.True(t, ok)
	assert.Equal(t, "gcp", id.Provider)
	assert.Equal(t, "1020227973377891447", id.InstanceID)
	assert.Equal(t, "us-central1", id.Region, "zone's trailing -<letter> suffix must be stripped to get the region")
}

func TestParseCloudInstanceIdentity_Azure(t *testing.T) {
	raw := "provider=azure\nsubscription_id=19e207a9-769d-4afd-b261-10bbed2d43e8\nresource_group=aks-dev-rg\nname=acme-dev\nlocation=eastus\npublic_ip="
	id, ok := parseCloudInstanceIdentity(raw)
	require.True(t, ok)
	assert.Equal(t, "azure", id.Provider)
	assert.Equal(t, "acme-dev", id.InstanceID)
	assert.Equal(t, "19e207a9-769d-4afd-b261-10bbed2d43e8", id.SubscriptionID)
	assert.Equal(t, "aks-dev-rg", id.ResourceGroup)
	assert.Equal(t, "eastus", id.Region)
}

func TestParseCloudInstanceIdentity_Azure_IncompleteFallsThrough(t *testing.T) {
	_, ok := parseCloudInstanceIdentity("provider=azure\nname=acme-dev")
	assert.False(t, ok, "azure identity needs subscription_id and resource_group too, not just name")
}

func TestParseCloudInstanceIdentity_NoProviderBlock(t *testing.T) {
	// Every curl attempt timed out (non-cloud/bare-metal host) — the collector
	// prints nothing.
	_, ok := parseCloudInstanceIdentity("")
	assert.False(t, ok)
}

func TestParseCloudInstanceIdentity_RegionDefaultsToGlobal(t *testing.T) {
	id, ok := parseCloudInstanceIdentity("provider=aws\ninstance_id=i-abc\nregion=")
	require.True(t, ok)
	assert.Equal(t, "global", id.Region)
}

func TestResourceShape_AWS(t *testing.T) {
	shape := cloudInstanceIdentity{Provider: "aws", InstanceID: "i-0aab26d051729d673"}.resourceShape()
	assert.Equal(t, "i-0aab26d051729d673", shape.ResourseID)
	assert.Equal(t, "compute-instance", shape.Type)
	assert.Equal(t, "AmazonEC2", shape.ServiceName)
	assert.Equal(t, "AWS", shape.CloudProvider)
	assert.Equal(t, "i-0aab26d051729d673", shape.ExternalResourceID, "a placeholder, not cloud-collector's ARN — corrected by reconcileExternalResourceIds on the next real sync")
}

func TestResourceShape_GCP(t *testing.T) {
	shape := cloudInstanceIdentity{Provider: "gcp", InstanceID: "1020227973377891447"}.resourceShape()
	assert.Equal(t, "1020227973377891447", shape.ResourseID)
	assert.Equal(t, "compute.googleapis.com/Instance", shape.Type)
	assert.Equal(t, "Compute Engine", shape.ServiceName)
	assert.Equal(t, "GCP", shape.CloudProvider)
	assert.Equal(t, "1020227973377891447", shape.ExternalResourceID, "a placeholder, not cloud-collector's ARN — corrected by reconcileExternalResourceIds on the next real sync")
}

func TestResourceShape_Azure_AssemblesAndLowercasesARMID(t *testing.T) {
	shape := cloudInstanceIdentity{
		Provider:       "azure",
		InstanceID:     "Acme-Dev",
		SubscriptionID: "19E207A9-769D-4AFD-B261-10BBED2D43E8",
		ResourceGroup:  "AKS-Dev-RG",
	}.resourceShape()
	want := "/subscriptions/19e207a9-769d-4afd-b261-10bbed2d43e8/resourcegroups/aks-dev-rg/providers/microsoft.compute/virtualmachines/acme-dev"
	assert.Equal(t, want, shape.ResourseID)
	assert.Equal(t, "Microsoft.Compute/virtualMachines", shape.Type)
	assert.Equal(t, "Azure", shape.CloudProvider)
	assert.Equal(t, want, shape.ExternalResourceID, "azure's external_resource_id is just the lowercased resourse_id, computable without cloud-collector's ARN-synthesis formula")
}

func TestResolveByIdentity_ExistingRowWins(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery("SELECT id::text FROM cloud_resourses").
		WithArgs("target-account", "tenant-1", "AWS", "i-0aab26d051729d673").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("resource-existing"))
	// adoptStalePlaceholder's lookup: empty rows -> sql.ErrNoRows -> no
	// pre-existing bare placeholder for this host, nothing to adopt.
	mock.ExpectQuery("SELECT id::text FROM cloud_resourses WHERE resourse_id").
		WithArgs("vm-172.31.0.11", "own-account", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	res, err := resolveByIdentity(mockDBManager, "target-account", "own-account", "tenant-1",
		cloudInstanceIdentity{Provider: "aws", InstanceID: "i-0aab26d051729d673", Region: "us-east-1"}, "172.31.0.11")
	require.NoError(t, err)
	assert.Equal(t, "resource-existing", res.CloudResourceID)
	assert.Equal(t, "target-account", res.AccountID)
}

func TestResolveByIdentity_CreatesWhenMissing(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery("SELECT id::text FROM cloud_resourses").
		WithArgs("target-account", "tenant-1", "AWS", "i-0aab26d051729d673").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec("INSERT INTO public.cloud_resourses").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id::text FROM cloud_resourses").
		WithArgs("target-account", "tenant-1", "AWS", "i-0aab26d051729d673").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("resource-new"))
	// adoptStalePlaceholder's lookup after creating the row too — a stale
	// placeholder could predate both cloud-collector's sync and this
	// identity collector's deployment.
	mock.ExpectQuery("SELECT id::text FROM cloud_resourses WHERE resourse_id").
		WithArgs("vm-172.31.0.11", "own-account", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	res, err := resolveByIdentity(mockDBManager, "target-account", "own-account", "tenant-1",
		cloudInstanceIdentity{Provider: "aws", InstanceID: "i-0aab26d051729d673", Region: "us-east-1"}, "172.31.0.11")
	require.NoError(t, err)
	assert.Equal(t, "resource-new", res.CloudResourceID)
}

func TestFindExistingResourceByMAC_FallsThroughToAzure(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery("cr.cloud_provider ILIKE 'aws'").
		WithArgs("account-1", "tenant-1", "aa:bb:cc:dd:ee:ff").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("vm.cloud_provider ILIKE 'azure'").
		WithArgs("account-1", "tenant-1", "aa:bb:cc:dd:ee:ff").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("azure-vm-1"))

	id, ok, err := findExistingResourceByMAC(mockDBManager, "account-1", "tenant-1", "aa:bb:cc:dd:ee:ff")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "azure-vm-1", id)
}

func TestFindExistingResourceByMAC_NoMatch(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery("cr.cloud_provider ILIKE 'aws'").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("vm.cloud_provider ILIKE 'azure'").WillReturnRows(sqlmock.NewRows([]string{"id"}))

	_, ok, err := findExistingResourceByMAC(mockDBManager, "account-1", "tenant-1", "aa:bb:cc:dd:ee:ff")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestFindExistingResourceByIP_ProviderFallbackChain(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery("cr.cloud_provider ILIKE 'aws'").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("cr.cloud_provider ILIKE 'gcp'").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("vm.cloud_provider ILIKE 'azure'").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("azure-vm-2"))

	id, ok, err := findExistingResourceByIP(mockDBManager, "account-1", "tenant-1", "172.31.0.12")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "azure-vm-2", id)
}

func TestAdoptStalePlaceholder_NoPlaceholder_NoOp(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery("SELECT id::text FROM cloud_resourses WHERE resourse_id").
		WithArgs("vm-172.31.0.11", "own-account", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	err := adoptStalePlaceholder(mockDBManager, "own-account", "tenant-1", "172.31.0.11", "resource-new")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAdoptStalePlaceholder_RepointsAndRetires(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery("SELECT id::text FROM cloud_resourses WHERE resourse_id").
		WithArgs("vm-172.31.0.11", "own-account", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("placeholder-old"))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE vm_package SET cloud_resource_id").
		WithArgs("resource-new", "placeholder-old").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("UPDATE recommendation SET resource_id").
		WithArgs("resource-new", "placeholder-old").
		WillReturnResult(sqlmock.NewResult(0, 5))
	mock.ExpectExec("UPDATE cloud_resourses SET is_active = false").
		WithArgs("placeholder-old").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := adoptStalePlaceholder(mockDBManager, "own-account", "tenant-1", "172.31.0.11", "resource-new")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestResolveTargets_UnassociatedDatasourceIsNoop(t *testing.T) {
	mock := withMockDB(t)
	// No DB calls expected at all — this is the assertion, via ExpectationsWereMet.

	resolved := resolveTargets(discardLogger, mockDBManager, "", "own-account", "tenant-1",
		[]SweepHost{{IP: "172.31.0.11", MAC: "aa:bb:cc:dd:ee:ff"}})
	assert.Empty(t, resolved)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestResolveTargets_ResolvesFromSweepHostCloudIdentity(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery("SELECT id::text FROM cloud_resourses").
		WithArgs("target-account", "tenant-1", "AWS", "i-0aab26d051729d673").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("resource-existing"))
	mock.ExpectQuery("SELECT id::text FROM cloud_resourses WHERE resourse_id").
		WithArgs("vm-172.31.0.11", "own-account", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	resolved := resolveTargets(discardLogger, mockDBManager, "target-account", "own-account", "tenant-1",
		[]SweepHost{{
			IP:            "172.31.0.11",
			CloudIdentity: "provider=aws\ninstance_id=i-0aab26d051729d673\nregion=us-east-1\n",
		}})
	require.Contains(t, resolved, "172.31.0.11")
	assert.Equal(t, "resource-existing", resolved["172.31.0.11"].CloudResourceID)
}

func TestResolveTargets_EmptyCloudIdentityFallsThroughToIP(t *testing.T) {
	mock := withMockDB(t)
	// Tier 0 skipped (no CloudIdentity), Tier 1 skipped (no MAC), Tier 2 (IP)
	// tried across all three providers with no match.
	mock.ExpectQuery("cr.cloud_provider ILIKE 'aws'").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("cr.cloud_provider ILIKE 'gcp'").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery("vm.cloud_provider ILIKE 'azure'").WillReturnRows(sqlmock.NewRows([]string{"id"}))

	resolved := resolveTargets(discardLogger, mockDBManager, "target-account", "own-account", "tenant-1",
		[]SweepHost{{IP: "172.31.0.12"}})
	assert.Empty(t, resolved, "no match on any tier must leave the host unresolved, not error")
}

func TestResolveTargets_OneHostErrorDoesNotBlockOthers(t *testing.T) {
	mock := withMockDB(t)
	// Host 1 (172.31.0.11): Tier 2 AWS lookup errors out.
	mock.ExpectQuery("cr.cloud_provider ILIKE 'aws'").WillReturnError(fmt.Errorf("connection reset"))
	// Host 2 (172.31.0.12): resolves normally via Tier 0 identity.
	mock.ExpectQuery("SELECT id::text FROM cloud_resourses").
		WithArgs("target-account", "tenant-1", "AWS", "i-0aab26d051729d673").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("resource-existing"))
	mock.ExpectQuery("SELECT id::text FROM cloud_resourses WHERE resourse_id").
		WithArgs("vm-172.31.0.12", "own-account", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	resolved := resolveTargets(discardLogger, mockDBManager, "target-account", "own-account", "tenant-1", []SweepHost{
		{IP: "172.31.0.11"},
		{IP: "172.31.0.12", CloudIdentity: "provider=aws\ninstance_id=i-0aab26d051729d673\nregion=us-east-1\n"},
	})
	assert.NotContains(t, resolved, "172.31.0.11", "the errored host must be left unresolved, not propagate the error")
	require.Contains(t, resolved, "172.31.0.12", "a later host's resolution must still run despite an earlier host's error")
	assert.Equal(t, "resource-existing", resolved["172.31.0.12"].CloudResourceID)
}

func TestResolveResourceIP_FromPrivateIpAddressMeta(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery("SELECT cloud_provider, meta::text").
		WithArgs("resource-1", "account-1", "tenant-1").
		WillReturnRows(sqlmock.NewRows([]string{"cloud_provider", "meta"}).
			AddRow("AWS", `{"PrivateIpAddress":"172.31.0.11","PublicIpAddress":"54.1.2.3"}`))

	ip, err := resolveResourceIP(mockDBManager, "resource-1", "account-1", "tenant-1")
	require.NoError(t, err)
	assert.Equal(t, "172.31.0.11", ip)
}

func TestResolveResourceIP_FallsBackToPublicWhenPrivateMissing(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery("SELECT cloud_provider, meta::text").
		WillReturnRows(sqlmock.NewRows([]string{"cloud_provider", "meta"}).
			AddRow("AWS", `{"PublicIpAddress":"54.1.2.3"}`))

	ip, err := resolveResourceIP(mockDBManager, "resource-1", "account-1", "tenant-1")
	require.NoError(t, err)
	assert.Equal(t, "54.1.2.3", ip)
}

func TestResolveResourceIP_NoneRecoverable(t *testing.T) {
	mock := withMockDB(t)
	mock.ExpectQuery("SELECT cloud_provider, meta::text").
		WillReturnRows(sqlmock.NewRows([]string{"cloud_provider", "meta"}).
			AddRow("AWS", nil))

	_, err := resolveResourceIP(mockDBManager, "resource-1", "account-1", "tenant-1")
	assert.Error(t, err)
}
