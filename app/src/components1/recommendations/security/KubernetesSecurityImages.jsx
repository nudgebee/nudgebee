import { useState, useEffect } from 'react';
import apiRecommendations from '@api1/recommendation';
import Datetime from '@components1/common/format/Datetime';
import { Box } from '@mui/material';
import SeverityInfographics from '@components1/k8s/common/SeverityInfographic';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import KubernetesSecurityDetails from './KubernetesSecurityDetails';
import InfographicList from '@components1/common/InfographicList';
import PropTypes from 'prop-types';
import { Text } from '@components1/common';
import CustomTable from '@components1/common/tables/CustomTable2';

const KubernetesSecurityImages = (props) => {
  const [loading, setLoading] = useState(false);
  const [tableData, setTableData] = useState([]);
  const [severityData, setSeverityData] = useState([]);
  const [dataCounts, setDataCounts] = useState([
    { text: 'Images', value: 0 },
    { text: 'Apps', value: 0 },
  ]);

  const listImagesSecurityData = () => {
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
    if (props?.query?.image) {
      query.image = props.query.image;
    }

    setLoading(true);
    setTableData([]);
    apiRecommendations
      .listImageSecurityRecommendation(props?.kubernetes?.id, query)
      .then((res) => {
        const securityAppsTableData = res?.recommendation_security_groupings_v2?.rows?.map((item) => {
          const data = [];
          data.push({
            component: <Text value={item?.image} showAutoEllipsis />,
            drilldownQuery: { image: item?.image, package_id: item?.package_id },
          });
          data.push({
            component: <Text value={item?.package_id} />,
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
          data.push({
            component: <Datetime baseDate={new Date()} value={item?.created_at} />,
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
          { text: 'Images', value: severityResponseData?.count_image },
          { text: 'Apps', value: severityResponseData?.count_workload_name },
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
    listImagesSecurityData();
  }, [props.kubernetes.id, JSON.stringify(props.query)]);
  return (
    <>
      {!props?.disableInfographic && (
        <Box sx={{ display: 'flex', justifyContent: 'space-between', mt: '12px' }}>
          <InfographicList sequence={dataCounts} />
          {severityData.length > 0 && <SeverityInfographics severityData={severityData} />}
        </Box>
      )}
      <CustomTable
        id={props.tableId}
        loading={loading}
        headers={[
          'Image',
          'Package ID',
          {
            component: (
              <Box display={'flex'} justifyContent={'center'}>
                <CustomLabels text={'Critical'} />{' '}
              </Box>
            ),
            width: '10%',
          },
          {
            component: (
              <Box display={'flex'} justifyContent={'center'}>
                <CustomLabels text={'High'} />
              </Box>
            ),
            width: '10%',
          },
          {
            component: (
              <Box display={'flex'} justifyContent={'center'}>
                <CustomLabels text={'Medium'} />
              </Box>
            ),
            width: '10%',
          },
          {
            component: (
              <Box display={'flex'} justifyContent={'center'}>
                <CustomLabels text={'Low'} />
              </Box>
            ),
            width: '10%',
          },
          'Created at',
        ]}
        tableData={tableData}
        totalRows={tableData?.length ?? 0}
        rowsPerPage={tableData?.length}
        resetPage={props?.resetPage || ''}
        showUpdatedEmptyData={tableData?.length == 0}
        showExpandable
        tableHeadingCenter={[]}
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
                      image: drilldownQuery?.image,
                      package_id: drilldownQuery.package_id,
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

export default KubernetesSecurityImages;

KubernetesSecurityImages.propTypes = {
  kubernetes: PropTypes.object,
  query: PropTypes.object,
  tableId: PropTypes.string,
  disableInfographic: PropTypes.bool,
  resetPage: PropTypes.string,
};
