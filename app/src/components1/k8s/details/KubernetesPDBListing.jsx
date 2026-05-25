import apiKubernetes from '@api1/kubernetes';
import { ListingLayout } from '@components1/ds/ListingLayout';
import DownloadButton from '@common-new/DownloadButton';
import CustomTable from '@common-new/tables/CustomTable2';
import { useEffect, useState } from 'react';
import PropTypes from 'prop-types';

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

const KubernetesPDBListing = ({ accountId }) => {
  const [data, setData] = useState([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    const fetchK8sData = async () => {
      setLoading(true);
      setData([]);

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
        const rows = items.map((item) => [
          { text: item?.metadata?.namespace ?? '' },
          { text: item?.metadata?.name ?? '' },
          { text: item?.status?.current_healthy ?? 0 },
          { text: item?.status?.disruptions_allowed ?? 0 },
        ]);
        setData(rows);
      } catch (error) {
        console.error('Error fetching data:', error);
      } finally {
        setLoading(false);
      }
    };

    fetchK8sData();
  }, [accountId]);

  return (
    <ListingLayout id='pdb-list'>
      <ListingLayout.Toolbar
        actions={
          <>
            <DownloadButton onClick={() => ({ tableId: 'pdb-list-table' })} />
          </>
        }
      />
      <ListingLayout.Body>
        <CustomTable
          id={'pdb-list-table'}
          tableData={data}
          headers={['Namespace', 'Name', 'Current Healthy', 'Disruption Allowed']}
          rowsPerPage={data.length}
          loading={loading}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

KubernetesPDBListing.propTypes = {
  accountId: PropTypes.string.isRequired,
};

export default KubernetesPDBListing;
