import apiRecommendations from '@api1/recommendation';
import { useEffect, useState } from 'react';
import Text from '@shared/format/Text';
import { Box } from '@mui/material';
import SeverityInfographics from '@components/k8s/common/SeverityInfographic';
import KubernetesSecurityDetails from './KubernetesSecurityDetails';
import { Label } from '@ui/Label';
import InfographicList from '@shared/widgets/InfographicList';
import PropTypes from 'prop-types';
import CustomTable from '@shared/tables/CustomTable2';

const KubernetesSecurityApps = (props) => {
  const [tableData, setTableData] = useState([]);
  const [loading, setLoading] = useState(false);
  const [severityData, setSeverityData] = useState([]);
  const [dataCounts, setDataCounts] = useState([
    { text: 'Images', value: 0 },
    { text: 'Apps', value: 0 },
  ]);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
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

    apiRecommendations
      .listAppSecurityRecommendation(props?.kubernetes?.id, query)
      .then((res) => {
        if (cancelled) return;
        const securityAppsTableData = res?.recommendation_security_groupings_v2?.rows?.map((item) => {
          const data = [];
          // A scanned-but-clean app (all its images are clean) comes back with every
          // severity count at 0. Flag it so it reads as "Clean" rather than a blank
          // 0/0/0/0 row. count_severity_info excludes Info/Unknown-only images.
          const isClean =
            (Number(item?.count_severity_critical) || 0) +
              (Number(item?.count_severity_high) || 0) +
              (Number(item?.count_severity_medium) || 0) +
              (Number(item?.count_severity_low) || 0) +
              (Number(item?.count_severity_info) || 0) ===
            0;
          data.push({
            component: (
              <Box>
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                  <Text value={item.workload_name} showAutoEllipsis />
                  {isClean && <Label tone='success' text='Clean' size='sm' />}
                </Box>
                {item.namespace && <Text secondaryText value={`ns: ${item.namespace}`} />}
              </Box>
            ),
            drilldownQuery: { workload_name: item.workload_name, namespace: item.namespace },
          });
          data.push({
            component: <Text value={item?.count_image} sx={{ textAlign: 'center' }} />,
          });
          data.push({
            component: <Text value={item?.count_severity_critical} sx={{ textAlign: 'center' }} />,
          });
          data.push({
            component: <Text value={item?.count_severity_high} sx={{ textAlign: 'center' }} />,
          });
          data.push({
            component: <Text value={item?.count_severity_medium} sx={{ textAlign: 'center' }} />,
          });
          data.push({
            component: <Text value={item?.count_severity_low} sx={{ textAlign: 'center' }} />,
          });
          return data;
        });
        setTableData(securityAppsTableData);
        setLoading(false);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [props.kubernetes.id, JSON.stringify(props.query)]);

  useEffect(() => {
    if (props?.disableInfographic) return;
    let cancelled = false;
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
    setDataCounts([
      { text: 'Images', value: '-' },
      { text: 'Apps', value: '-' },
    ]);
    setSeverityData([
      { label: 'Critical', value: '-' },
      { label: 'High', value: '-' },
      { label: 'Medium', value: '-' },
      { label: 'Low', value: '-' },
    ]);
    apiRecommendations.getSecuritySeverityGrouping(query).then((res) => {
      if (cancelled) return;
      const severityResponseData = res?.recommendation_security_groupings_v2?.rows[0];
      setSeverityData([
        { label: 'Critical', value: severityResponseData?.count_severity_critical },
        { label: 'High', value: severityResponseData?.count_severity_high },
        { label: 'Medium', value: severityResponseData?.count_severity_medium },
        { label: 'Low', value: severityResponseData?.count_severity_low },
      ]);
      setDataCounts([
        { text: 'Images', value: severityResponseData?.count_image },
        { text: 'Apps', value: severityResponseData?.count_workload_name },
      ]);
    });
    return () => {
      cancelled = true;
    };
  }, [props.query.status, props.kubernetes.id, props.query.namespace, props.query.workload_name, props.query.severity]);

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
        tableData={tableData}
        resetPage={props?.resetPage || ''}
        headers={[
          { name: 'Application', width: '25%' },
          { name: 'Images count', width: '15%' },
          {
            component: (
              <Box display={'flex'} justifyContent={'center'}>
                <Label text={'Critical'} />{' '}
              </Box>
            ),
            width: '10%',
          },
          {
            component: (
              <Box display={'flex'} justifyContent={'center'}>
                <Label text={'High'} />
              </Box>
            ),
            width: '10%',
          },
          {
            component: (
              <Box display={'flex'} justifyContent={'center'}>
                <Label text={'Medium'} />
              </Box>
            ),
            width: '10%',
          },
          {
            component: (
              <Box display={'flex'} justifyContent={'center'}>
                <Label text={'Low'} />
              </Box>
            ),
            width: '10%',
          },
        ]}
        showUpdatedEmptyData={tableData?.length == 0}
        showExpandable
        expandable={{
          tabs: [
            {
              text: 'Details',
              value: 0,
              componentFn: function (opt, drilldownQuery, _row) {
                return (
                  <KubernetesSecurityDetails
                    disableInfographic
                    kubernetes={props?.kubernetes}
                    query={{
                      workload_name: drilldownQuery?.workload_name,
                      namespace: drilldownQuery?.namespace,
                      status: props?.query.status,
                      severity: props?.query.severity,
                    }}
                    workload_name={drilldownQuery?.workload_name}
                  />
                );
              },
            },
          ],
        }}
        totalRows={tableData?.length}
        rowsPerPage={tableData?.length}
        disableInfographic
        tableHeadingCenter={['Images count']}
      />
    </>
  );
};

export default KubernetesSecurityApps;

KubernetesSecurityApps.propTypes = {
  kubernetes: PropTypes.object,
  query: PropTypes.object,
  tableId: PropTypes.string,
  disableInfographic: PropTypes.bool,
  resetPage: PropTypes.string,
};
