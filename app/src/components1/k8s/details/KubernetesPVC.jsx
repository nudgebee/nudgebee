import React, { useState, useEffect } from 'react';
import BoxLayout2 from '@components1/common/BoxLayout2';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import k8sApi from '@api1/kubernetes';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { Box, Grid, Typography } from '@mui/material';
import { Modal } from '@components1/common/modal';
import Datetime from '@components1/common/format/Datetime';
import PropTypes from 'prop-types';
import { getAllowedNamespaces, hasWriteAccess } from '@lib/auth';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import recommendationApi from '@api1/recommendation';
import AutoOptimizePVRightSizingSingleConfiguration from '@components1/autopilot/form/AutoOptimizePVRightSizingSingleConfiguration';
import { Text, ThreeDotsMenu } from '@components1/common';
import { action } from 'src/utils/actionStyles';
import AutoPilotSettingIcon from '@assets/application/auto-pilot-new.svg';
import { DeleteIconRed as DeleteIcon } from '@assets';
import EditFileIcon from '@assets/application/edit-new.svg';

const NAMESPACE_HEADERS = ['Name', 'Namespace', 'Status', 'Capacity', 'StorageClass', 'AccessMode', 'Age', ''];

function parseK8sDate(date) {
  return new Date(date?.replace(' ', 'T'));
}

const KubernetesPVCTable = ({ accountId }) => {
  const router = useRouter();

  const [data, setData] = useState([]);
  const [totalCount, setTotalCount] = useState(0);
  const [loading, setLoading] = useState(false);
  const [namespaceFilter, setNamespaceFilter] = useState([]);
  const [selectedNamespace, setSelectedNamespace] = useState(router.query.namespace || '');
  const [filteredData, setFilteredData] = useState([]);
  const [isAutoPilotModalOpen, setIsAutoPilotModalOpen] = useState(false);
  const [autoPilotModalData, setAutoPilotModalData] = useState({});
  const [fetchRecommendationAutoPilot, setFetchRecommendationAutoPilot] = useState(false);

  const kubernetesPVCTable = 'kubernetesPVCTable';

  const closeAutoPilotModal = () => {
    setIsAutoPilotModalOpen(false);
  };

  const onMenuClick = (item) => {
    if (item.id === '0') {
      setAutoPilotModalData(data);
      setIsAutoPilotModalOpen(true);
    }
  };

  function getMenuItems(item, rightSizeCounts) {
    if (!hasWriteAccess(accountId, item.metadata.namespace)) {
      return [];
    }

    let menus = [
      {
        icon: AutoPilotSettingIcon,
        label: 'Auto Optimize',
        id: '0',
        disabled: rightSizeCounts > 0,
      },
      {
        icon: DeleteIcon,
        label: 'Delete',
        disabled: true,
        id: '1',
      },
      {
        icon: EditFileIcon,
        label: 'Edit',
        disabled: true,
        id: '2',
      },
    ];
    return menus;
  }

  useEffect(() => {
    if (!accountId) {
      return;
    }
    setLoading(true);
    k8sApi
      .relayForwardRequest({
        no_sinks: true,
        cache: false,
        body: {
          account_id: accountId,
          action_name: 'get_resource',
          action_params: {
            group: '',
            version: 'v1',
            resource_type: 'persistentvolumeclaims',
            all_namespaces: true,
          },
        },
      })
      .then((res) => {
        let data = res?.data?.findings?.[0]?.evidence?.[0]?.data;
        if (data) {
          try {
            let parsedData = JSON.parse(data);
            data = parsedData[0].data;
          } catch (e) {
            console.error('Error parsing data', e);
          }
        }
        if (typeof data === 'string') {
          data = JSON.parse(data);
        }
        let allowedNamespace = getAllowedNamespaces(accountId);
        if (allowedNamespace != null && allowedNamespace.length > 0) {
          data = data.filter((item) => allowedNamespace.includes(item.metadata.namespace));
        }
        let namespaces = data?.map((item) => item.metadata.namespace);
        setNamespaceFilter([...new Set(namespaces)]);

        const tableData = data?.map((item) => {
          return [
            {
              component: <Text value={item.metadata.name} showAutoEllipsis />,
              drilldownQuery: {
                data: item,
                pvcName: item.metadata.name,
                pvName: item.spec.volume_name,
                namespaceName: item.metadata.namespace,
              },
            },
            {
              component: <Text value={item.metadata.namespace} />,
            },
            {
              component: <Text value={item.status.phase} />,
            },
            {
              component: <Text value={item.status.capacity?.storage ?? '-'} />,
            },
            {
              component: <Text value={item.spec.storage_class_name} />,
            },
            {
              component: <Text value={item.spec.access_modes?.join(',')} />,
            },
            {
              component: <Datetime value={parseK8sDate(item.metadata.creation_timestamp)} />,
            },
            {
              component: (
                <Box display={'flex'} justifyContent={'flex-end'} alignItems={'center'}>
                  <ThreeDotsMenu sx={{ ...action.primary }} menuItems={getMenuItems(item, 0)} data={item} onMenuClick={onMenuClick} />
                </Box>
              ),
            },
          ];
        });
        setData(tableData ?? []);
        setFetchRecommendationAutoPilot(true);
      })
      .finally(() => {
        setLoading(false);
      });
  }, [accountId]);

  // Client-side namespace filtering — no API call needed
  useEffect(() => {
    if (!data.length) return;
    const ns = router.query.namespace;
    if (ns) {
      const newData = data.filter((item) => item[0].drilldownQuery.data.metadata.namespace === ns);
      setFilteredData(newData);
      setTotalCount(newData.length);
    } else {
      setFilteredData([...data]);
      setTotalCount(data.length);
    }
  }, [router.query.namespace, data]);

  useEffect(() => {
    if (!accountId) {
      return;
    }
    if (fetchRecommendationAutoPilot) {
      recommendationApi
        .getAutoOptimize({ accountId: accountId, category: ['pvc_rightsize'], status: 'Active' })
        .then((res) => {
          const listAutoPilots = res?.data?.auto_pilot ?? [];
          if (listAutoPilots.length > 0) {
            for (let i = 0; i < data.length; i++) {
              let count = 0;
              for (let a of listAutoPilots) {
                let hasAutopilotConfigured = false;
                for (let r of a.auto_optimize_resource_maps) {
                  if (
                    r?.resource_identifier?.name == data[i][0].drilldownQuery.data.metadata.name &&
                    r?.resource_identifier?.namespace == data[i][0].drilldownQuery.data.metadata.namespace
                  ) {
                    hasAutopilotConfigured = true;
                    break;
                  }
                  if (
                    r?.resource_identifier?.name == null &&
                    r?.resource_identifier?.namespace == data[i][0].drilldownQuery.data.metadata.namespace
                  ) {
                    hasAutopilotConfigured = true;
                    break;
                  }
                }
                if (hasAutopilotConfigured) {
                  count += 1;
                }
              }
              data[i][7].component = (
                <Box display={'flex'} justifyContent={'flex-end'} alignItems={'center'}>
                  <ThreeDotsMenu
                    sx={{ ...action.primary }}
                    menuItems={getMenuItems(data[i][0].drilldownQuery.data, count)}
                    data={data[i][0].drilldownQuery.data}
                    onMenuClick={onMenuClick}
                  />
                </Box>
              );
            }
            setData([...data]);
          }
        })
        .finally(() => {
          setFetchRecommendationAutoPilot(false);
        });
    }
  }, [fetchRecommendationAutoPilot]);

  const onNamespaceFilterChange = (e) => {
    setSelectedNamespace(e?.target?.value);
    applyFiltersOnRouter(router, { namespace: e?.target?.value });
    if (e?.target?.value) {
      let newData = data.filter((item) => item[0].drilldownQuery.data.metadata.namespace === e?.target?.value);
      setFilteredData(newData);
      setTotalCount(newData?.length);
    } else {
      setFilteredData([...data]);
      setTotalCount(data?.length);
    }
  };

  return (
    <BoxLayout2
      id='all-namespaces'
      heading=''
      filterOptions={[
        {
          type: 'dropdown',
          enabled: true,
          options: namespaceFilter,
          onSelect: onNamespaceFilterChange,
          minWidth: '150px',
          label: 'Namespace',
          value: selectedNamespace,
        },
      ]}
      dateTimeRange={{
        enabled: false,
      }}
      sharingOptions={{
        download: {
          enabled: true,
          onClick: () => {
            return {
              tableId: kubernetesPVCTable,
            };
          },
        },
        sharing: { enabled: true },
      }}
    >
      <KubernetesTable2
        id={kubernetesPVCTable}
        headers={NAMESPACE_HEADERS}
        data={filteredData}
        expandable={{
          tabs: [
            {
              text: 'Details',
              value: 0,
              key: 'WorkloadDetails',
              componentFn: pvDetailsFn,
            },
            { text: 'Utilization Trends', value: 2, key: 'pvc_utilization' },
          ],
        }}
        rowsPerPage={totalCount}
        totalRows={totalCount}
        showExpandable
        loading={loading}
        stickyColumnIndex='8'
      />
      <Modal width='md' open={isAutoPilotModalOpen} handleClose={closeAutoPilotModal} title={'Auto Optimize - PV Rightsizing'}>
        <AutoOptimizePVRightSizingSingleConfiguration
          autoOptimizeData={{
            auto_optimize_resource_maps: [
              {
                resource_identifier: {
                  namespace: autoPilotModalData?.metadata?.namespace,
                  name: autoPilotModalData?.metadata?.name,
                  type: 'PersistenceVolumeClaim',
                },
              },
            ],
          }}
          closeAutoPilotSingleConfigModal={closeAutoPilotModal}
          msTeamsData={[]}
          googleChannelList={[]}
          listAutoPilot={[]}
          setIsLoading={setLoading}
          currentData={{}}
        />
      </Modal>
    </BoxLayout2>
  );
};

function pvDetailsFn(accountId, drilldownQuery) {
  const mapLabels = (label) => {
    if (!label) {
      return [];
    }
    const labelArray = [];

    for (let [k, v] of Object.entries(label)) {
      let name = k + '=' + v;
      labelArray.push(<CustomLabels height='auto' margin='0px' wordBreak={''} displayTooltip key={k} text={name} variant={'grey'} />);
    }
    return labelArray;
  };

  return (
    <Box>
      <Grid container sx={{ marginBottom: '8px' }}>
        <Grid item md={3}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Labels:
          </Typography>
        </Grid>
        <Grid
          item
          md={9}
          sx={{
            display: 'flex',
            flexDirection: 'row',
            flexWrap: 'wrap',
            gap: '12px',
            fontFamily: 'Roboto',
            fontSize: '14px',
            fontWeight: '500',
            lineHeight: '20px',
            color: '#2563EB',
            maxWidth: '360px',
          }}
        >
          {mapLabels(drilldownQuery?.data?.metadata?.labels) ?? []}
        </Grid>
      </Grid>
      <Grid container sx={{ marginBottom: '8px' }}>
        <Grid item md={3}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Annotations:
          </Typography>
        </Grid>
        <Grid
          item
          md={9}
          sx={{
            display: 'flex',
            flexDirection: 'row',
            flexWrap: 'wrap',
            fontFamily: 'Roboto',
            gap: '12px',
            fontSize: '14px',
            fontWeight: '500',
            lineHeight: '20px',
            color: '#2563EB',
            maxWidth: '360px',
          }}
        >
          {mapLabels(drilldownQuery?.data?.metadata?.annotations) ?? []}
        </Grid>
      </Grid>
      <Grid container sx={{ marginBottom: '8px' }}>
        <Grid item md={3}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Finalizers:
          </Typography>
        </Grid>
        <Grid
          item
          md={9}
          sx={{
            display: 'flex',
            flexDirection: 'row',
            flexWrap: 'wrap',
            fontFamily: 'Roboto',
            gap: '12px',
            fontSize: '14px',
            fontWeight: '500',
            lineHeight: '20px',
            color: '#2563EB',
            maxWidth: '360px',
          }}
        >
          {drilldownQuery?.data?.metadata?.finalizers?.join(',')}
        </Grid>
      </Grid>{' '}
      <Grid container sx={{ marginBottom: '8px' }}>
        <Grid item md={3}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Volume Name:
          </Typography>
        </Grid>
        <Grid
          item
          md={9}
          sx={{
            display: 'flex',
            flexDirection: 'row',
            flexWrap: 'wrap',
            fontFamily: 'Roboto',
            gap: '12px',
            fontSize: '14px',
            fontWeight: '500',
            lineHeight: '20px',
            color: '#2563EB',
            maxWidth: '360px',
          }}
        >
          {drilldownQuery?.data?.spec?.volume_name}
        </Grid>
      </Grid>
      <Grid container sx={{ marginBottom: '8px' }}>
        <Grid item md={3}>
          <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
            Volume Mode:
          </Typography>
        </Grid>
        <Grid
          item
          md={9}
          sx={{
            display: 'flex',
            flexDirection: 'row',
            flexWrap: 'wrap',
            fontFamily: 'Roboto',
            gap: '12px',
            fontSize: '14px',
            fontWeight: '500',
            lineHeight: '20px',
            color: '#2563EB',
            maxWidth: '360px',
          }}
        >
          {drilldownQuery?.data?.spec?.volume_mode}
        </Grid>
      </Grid>
    </Box>
  );
}

KubernetesPVCTable.propTypes = {
  accountId: PropTypes.string.isRequired,
};

export default KubernetesPVCTable;
