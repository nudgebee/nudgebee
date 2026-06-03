import apiKubernetes from '@api1/kubernetes';
import TraceIcon from '@assets/kubernetes/trace-icon.svg';
import Text from '@common-new/format/Text';
import { ListingLayout } from '@components1/ds/ListingLayout';
import DownloadButton from '@common-new/DownloadButton';
import CustomTable from '@common-new/tables/CustomTable2';

// Lists PDBs via the generic `get_resource` primitive against the policy/v1
// GVR. Status fields come back snake_cased from the agent's kube handler.
const safelyParseJson = (jsonString) => {
  try {
    return JSON.parse(jsonString);
  } catch (error) {
    console.error('Error parsing JSON:', error);
    return null;
  }
};

const extractPdbItems = (response) => {
  const evidence = response?.data?.findings?.[0]?.evidence?.[0]?.data;
  if (!evidence) {
    return [];
  }
  const parsed = safelyParseJson(evidence);
  const inner = parsed?.[0]?.data;
  if (!inner) {
    return [];
  }
  const items = typeof inner === 'string' ? safelyParseJson(inner) : inner;
  return Array.isArray(items) ? items : [];
};

class Pdb {
  constructor() {
    this.id = 'Pdb';
    this.icon = TraceIcon;
    this.text = 'PDB Check';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.accountId = '';
    this.pdbList = [];
  }

  canRenderContent = async (accountId) => {
    this.renderContent = true;
    this.accountId = accountId;
    // Reset per-fetch state so navigating between accounts in the same
    // session doesn't accumulate insights / table rows from prior loads.
    this.insightData = [];
    this.pdbList = [];

    try {
      const response = await apiKubernetes.relayForwardRequest({
        no_sinks: true,
        body: {
          account_id: accountId,
          action_name: 'get_resource',
          action_params: {
            group: 'policy',
            version: 'v1',
            resource_type: 'poddisruptionbudgets',
            all_namespaces: true,
          },
          origin: 'Nudgebee UI',
        },
      });
      const items = extractPdbItems(response);
      if (items.length === 0) {
        return this.renderContent;
      }
      const rows = items.map((item) => ({
        namespace: item?.metadata?.namespace ?? '',
        name: item?.metadata?.name ?? '',
        currentHealthy: item?.status?.current_healthy ?? 0,
        disruptionsAllowed: item?.status?.disruptions_allowed ?? 0,
      }));
      // Flag only PDBs that are actively blocking eviction: at least one
      // healthy pod with zero disruptions allowed. PDBs with currentHealthy=0
      // are typically targeting workloads with no running replicas and aren't
      // a disruption hazard during an upgrade.
      const blocked = rows.filter((r) => r.disruptionsAllowed === 0 && r.currentHealthy > 0);
      if (blocked.length > 0) {
        this.insightData.push({
          message: `There are ${blocked.length} PDBs blocking disruption (healthy pods with disruptions_allowed=0).`,
          component: null,
          severity: 'Critical',
        });
      }
      this.pdbList = rows.map((row) => {
        const isDisruptionNotAllowed = row.disruptionsAllowed === 0 && row.currentHealthy !== row.disruptionsAllowed;
        const textColor = isDisruptionNotAllowed ? 'var(--ds-red-600)' : 'inherit';
        return [
          { component: <Text value={row.namespace} sx={{ color: textColor }} /> },
          { component: <Text value={row.name} sx={{ color: textColor }} /> },
          { text: row.currentHealthy },
          { text: row.disruptionsAllowed },
        ];
      });
    } catch (error) {
      console.error('Error fetching pdb list:', error);
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderPdb()];
  };

  renderPdb = () => {
    return (
      <ListingLayout id='pdb-list'>
        <ListingLayout.Toolbar actions={<DownloadButton onClick={() => ({ tableId: 'pdb-list-table' })} />} />
        <ListingLayout.Body>
          <CustomTable
            id={'pdb-list-table'}
            tableData={this.pdbList}
            headers={['Namespace', 'Name', 'Current Healthy', 'Disruption Allowed']}
            rowsPerPage={this.pdbList.length}
            loading={false}
          />
        </ListingLayout.Body>
      </ListingLayout>
    );
  };
}

export default Pdb;
