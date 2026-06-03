import { useState, useEffect } from 'react';
import apiRecommendations from '@api1/recommendation';
import { Box } from '@mui/material';
import SeverityInfographics from '@components1/k8s/common/SeverityInfographic';
import KubernetesSecurityDetails from './KubernetesSecurityDetails';
import InfographicList from '@components1/common/InfographicList';
import PropTypes from 'prop-types';
import Text from '@common-new/format/Text';
import CustomTable from '@common-new/tables/CustomTable2';
import { SeverityIcon } from '@components1/ds/SeverityIcon';

const SEVERITY_TO_DS_LEVEL = {
  critical: 'critical',
  high: 'high',
  medium: 'medium',
  low: 'low',
  info: 'info',
};
const toDsSeverityLevel = (s) => SEVERITY_TO_DS_LEVEL[String(s || '').toLowerCase()] || 'info';

const KubernetesSecurityCVE = (props) => {
  const [loading, setLoading] = useState(false);
  const [tableData, setTableData] = useState([]);
  const [severityData, setSeverityData] = useState([]);
  const [dataCounts, setDataCounts] = useState([
    { text: 'CVE', value: 0 },
    { text: 'Images', value: 0 },
  ]);

  const listCVESecurityData = () => {
    const query = {};
    if (props?.query.workload_name) {
      query.workload_name = props?.query.workload_name;
    }
    if (props?.query?.namespace) {
      query.namespace = props?.query.namespace;
    }
    if (props?.query?.severity) {
      query.severity = props?.query.severity;
    }
    if (props?.query?.status) {
      query.status = props?.query.status;
    }
    setLoading(true);
    apiRecommendations
      .listCVESecurityRecommendation(props?.kubernetes?.id, query)
      .then((res) => {
        const securityAppsTableData = res?.recommendation_security_groupings_v2?.rows?.map((item) => {
          const data = [];
          data.push({
            component: <Text value={item?.vulnerability_id} />,
            drilldownQuery: { vulnerabilityId: item?.vulnerability_id },
          });
          data.push({
            component: <Text value={item?.count_image} />,
          });
          data.push({
            component: <Text value={item?.count_workload_name} />,
          });
          data.push({
            component: <Text value={item?.count} />,
          });
          data.push({
            component: <SeverityIcon level={toDsSeverityLevel(item?.severity)} aria-label={item?.severity || '-'} />,
            data: item?.severity,
          });
          return data;
        });
        setTableData(securityAppsTableData);
        setLoading(false);
      })
      .catch(() => {
        setLoading(false);
      });
  };

  const listSeverityInfographics = () => {
    const query = { accountId: props.kubernetes.id };
    if (props?.query.workload_name) {
      query.workload = props?.query.workload_name;
    }
    if (props?.query?.namespace) {
      query.namespace = props?.query.namespace;
    }
    if (props?.query?.status) {
      query.status = props?.query.status;
    }
    if (props?.query?.severity) {
      query.severity = props?.query.severity;
    }
    setSeverityData([
      { label: 'Critical', value: '-' },
      { label: 'High', value: '-' },
      { label: 'Medium', value: '-' },
      { label: 'Low', value: '-' },
    ]);
    setDataCounts([
      { text: 'Images', value: '-' },
      { text: 'Apps', value: '-' },
    ]);
    apiRecommendations
      .getSecuritySeverityGrouping(query)
      .then((res) => {
        const severityResponseData = res?.recommendation_security_groupings_v2?.rows[0];
        setSeverityData([
          { label: 'Critical', value: severityResponseData?.count_severity_critical },
          { label: 'High', value: severityResponseData?.count_severity_high },
          { label: 'Medium', value: severityResponseData?.count_severity_medium },
          { label: 'Low', value: severityResponseData?.count_severity_low },
        ]);
        setDataCounts([
          { text: 'CVE', value: severityResponseData?.count_vulnerability_id },
          { text: 'Images', value: severityResponseData?.count_image },
        ]);
      })
      .catch((err) => {
        console.error(err);
      });
  };

  useEffect(() => {
    if (!props?.disableInfographic) {
      listSeverityInfographics();
    }
  }, [props.kubernetes.id, JSON.stringify(props.query)]);

  useEffect(() => {
    listCVESecurityData();
  }, [props.kubernetes.id, JSON.stringify(props.query)]);

  return (
    <>
      {!props?.disableInfographic && (
        <Box sx={{ display: 'flex', justifyContent: 'space-between', mt: 'var(--ds-space-3)' }}>
          <InfographicList sequence={dataCounts} />
          {severityData.length > 0 && <SeverityInfographics severityData={severityData} />}
        </Box>
      )}
      <CustomTable
        id={props.tableId}
        loading={loading}
        headers={['CVE', 'Images', 'Applications', 'Count', 'Severity']}
        tableData={tableData}
        totalRows={tableData?.length}
        rowsPerPage={tableData?.length}
        resetPage={props?.resetPage || ''}
        showUpdatedEmptyData={tableData?.length == 0}
        showExpandable
        expandable={{
          tabs: [
            {
              text: 'Details',
              value: 0,
              componentFn: function (e, drilldownQuery) {
                return (
                  <KubernetesSecurityDetails
                    kubernetes={props?.kubernetes}
                    query={{
                      workload_name: drilldownQuery?.workload_name,
                      namespace: drilldownQuery?.namespace,
                      vulnerabilityId: drilldownQuery?.vulnerabilityId,
                      status: props?.query.status,
                    }}
                    disableInfographic
                  />
                );
              },
            },
          ],
        }}
      />
    </>
  );
};

export default KubernetesSecurityCVE;

KubernetesSecurityCVE.propTypes = {
  kubernetes: PropTypes.object,
  query: PropTypes.object,
  tableId: PropTypes.string,
  disableInfographic: PropTypes.bool,
  resetPage: PropTypes.string,
};
