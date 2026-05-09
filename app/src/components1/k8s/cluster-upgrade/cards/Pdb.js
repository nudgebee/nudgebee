import apiKubernetes from '@api1/kubernetes';
import TraceIcon from '@assets/kubernetes/trace-icon.svg';
import { Text } from '@components1/common';
import BoxLayout2 from '@components1/common/BoxLayout2';
import CustomTable from '@components1/common/tables/CustomTable2';

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
    const createRequestData = (accountId) => ({
      no_sinks: true,
      body: {
        account_id: accountId,
        action_name: 'list_k8s_pdb',
        action_params: { a: 'b' },
        origin: 'Nudgebee UI',
      },
    });

    const safelyParseJson = (jsonString) => {
      try {
        return JSON.parse(jsonString);
      } catch (error) {
        console.error('Error parsing JSON:', error);
        return null;
      }
    };

    const handleResponse = (response) => {
      const findings = response?.data?.findings ?? [];
      if (findings.length > 0) {
        processFindings(findings[0]);
      }
    };

    const processFindings = (finding) => {
      const evidence = finding?.evidence ?? [];
      if (evidence.length > 0) {
        processEvidence(evidence[0]?.data);
      }
    };

    const processEvidence = (evidenceData) => {
      const parsedEvidence = safelyParseJson(evidenceData);
      if (parsedEvidence && parsedEvidence.length > 0) {
        const detailedData = safelyParseJson(parsedEvidence[0]?.data) ?? [];
        if (detailedData.length > 0) {
          const disruptionFiltered = detailedData.filter((item) => item.disruptionsAllowed == 0).map(({ namespace, name }) => ({ namespace, name }));
          if (disruptionFiltered.length > 0) {
            const message = `There are ${disruptionFiltered.length} PDBs with no disruptions allowed.`;
            const component = null;
            this.insightData.push({
              message,
              component,
              severity: 'Critical',
            });
          }
          const formatData = (data) =>
            data.map((item) => {
              const isDisruptionNotAllowed = item.disruptionsAllowed == 0 && item.currentHealthy != item.disruptionsAllowed;
              const textColor = isDisruptionNotAllowed ? 'red' : 'default';

              return [
                { component: <Text value={item.namespace} sx={{ color: textColor }} /> },
                { component: <Text value={item.name} sx={{ color: textColor }} /> },
                { text: item.currentHealthy },
                { text: item.disruptionsAllowed },
              ];
            });
          this.pdbList = formatData(detailedData);
          this.renderContent = true;
        }
      }
    };

    try {
      const requestData = createRequestData(accountId);
      const response = await apiKubernetes.relayForwardRequest(requestData);
      handleResponse(response);
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
      <BoxLayout2
        id='pdb-list'
        sharingOptions={{
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: 'pdb-list-table',
              };
            },
          },
          sharing: { enabled: true },
        }}
      >
        <CustomTable
          id={'pdb-list-table'}
          tableData={this.pdbList}
          headers={['Namespace', 'Name', 'Current Healthy', 'Disruption Allowed']}
          rowsPerPage={this.pdbList.length}
          loading={false}
        />
      </BoxLayout2>
    );
  };
}

export default Pdb;
