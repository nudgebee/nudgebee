import apiKubernetes from '@api1/kubernetes';
import { BoxLayout2 } from '@components1/common';
import CustomTable from '@components1/common/tables/CustomTable2';
import { useEffect, useState } from 'react';
import PropTypes from 'prop-types';

const KubernetesPDBListing = ({ accountId }) => {
  const [data, setData] = useState([]);
  const [loading, setLoading] = useState(false);

  const safelyParseJson = (jsonString) => {
    try {
      return JSON.parse(jsonString);
    } catch (error) {
      console.error('Error parsing JSON:', error);
      return null;
    }
  };

  const formatData = (data) =>
    data.map((item) => [{ text: item.namespace }, { text: item.name }, { text: item.currentHealthy }, { text: item.disruptionsAllowed }]);

  useEffect(() => {
    const fetchK8sData = async () => {
      setLoading(true);
      setData([]);

      try {
        const requestData = createRequestData(accountId);
        const response = await apiKubernetes.relayForwardRequest(requestData);
        handleResponse(response);
      } catch (error) {
        console.error('Error fetching data:', error);
      } finally {
        setLoading(false);
      }
    };

    const createRequestData = (accountId) => ({
      no_sinks: true,
      body: {
        account_id: accountId,
        action_name: 'list_k8s_pdb',
        action_params: { a: 'b' },
        origin: 'Nudgebee UI',
      },
    });

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
          setData(formatData(detailedData));
        }
      }
    };

    fetchK8sData();
  }, [accountId]);

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
        tableData={data}
        headers={['Namespace', 'Name', 'Current Healthy', 'Disruption Allowed']}
        rowsPerPage={data.length}
        loading={loading}
      />
    </BoxLayout2>
  );
};

KubernetesPDBListing.propTypes = {
  accountId: PropTypes.string.isRequired,
};

export default KubernetesPDBListing;
