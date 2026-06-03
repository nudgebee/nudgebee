import { Tooltip, Box, Typography, Grid } from '@mui/material';
import { Divider } from '@components1/ds/Divider';
import React, { useEffect, useState } from 'react';
import OnDemandIcon from '@assets/on-demand-icon.svg';
import FallbackIcon from '@assets/fallback-icon.svg';
import SpotIcon from '@assets/spot-icon.svg';
import ValueWithHeading from './ValueWithHeading';
import SafeIcon from '@components1/common/SafeIcon';
import PropTypes from 'prop-types';
import { Modal } from '@components1/ds/Modal';
import CustomTable from '@common-new/tables/CustomTable2';
import k8sApi from '@api1/kubernetes';
import { ds } from '@utils/colors';

const NodeList = ({ nodeData, showNodes }) => {
  const [nodesModal, setNodesModal] = useState(false);
  const [applicationEventData, _setApplicationEventData] = useState([]);

  //application events

  const closeNodesModal = () => {
    setNodesModal(false);
  };

  return (
    <>
      <Modal
        width='sm'
        open={nodesModal}
        handleClose={closeNodesModal}
        title={
          <Box display={'flex'} alignItems={'center'} gap={ds.space.mul(0, 5)} fontSize={ds.text.title} fontWeight={600} color={ds.gray[700]}>
            Instances
          </Box>
        }
        contentStyles={{
          padding: 'var(--ds-space-5) var(--ds-space-6)',
        }}
      >
        <CustomTable
          tableData={applicationEventData}
          rowsPerPage={applicationEventData.length}
          headers={['Instances', 'Count']}
          showUpdatedTable
          showEmptyStateText
        />
      </Modal>
      <Box sx={{ width: '100%', mt: 'var(--ds-space-2)' }}>
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr', gap: 'var(--ds-space-1)' }}>
          {showNodes &&
            Object.entries(nodeData?.nodeTypes ?? {}).map(([key, value]) => {
              return (
                <Grid container key={key} justifyContent='space-between'>
                  <Grid item>
                    <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-regular)', color: 'var(--ds-gray-400)' }}>
                      {key}
                    </Typography>
                  </Grid>
                  <Grid item>
                    <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-brand-500)' }}>
                      {value.count}{' '}
                      <span style={{ color: 'var(--ds-gray-400)', fontWeight: 'var(--ds-font-weight-regular)' }}>({value.spotCount} spot)</span>
                    </Typography>
                  </Grid>
                </Grid>
              );
            })}
        </Box>
      </Box>
    </>
  );
};

NodeList.propTypes = {
  nodeListData: PropTypes.array,
};

const ClusterNode = ({
  width,
  node = {},
  sort = {},
  largeVariant,
  clusterSummary = false,
  updatedNode = false,
  accountId = '',
  showNodes = true,
}) => {
  const { demand, spot, fallback } = node;
  const total = (demand ?? 0) + (spot ?? 0) + (fallback ?? 0);
  const demandPercentage = `${(demand / total) * 100}%`;
  const spotPercentage = `${(spot / total) * 100}%`;
  const fallbackPercentage = `${(fallback / total) * 100}%`;

  const [nodeDistribution, setNodeDistribution] = useState({});

  useEffect(() => {
    if (showNodes) {
      k8sApi
        .getK8sNodes({
          accountId,
          isActive: true,
        })
        .then((res) => {
          let nodeDistibution = { nodeTypes: {} };

          res.data.k8s_nodes?.map((item) => {
            if (item.node_type in nodeDistibution) {
              nodeDistibution[item.node_type?.toLowerCase()] += 1;
            } else {
              nodeDistibution[item.node_type?.toLowerCase()] = 1;
            }

            if (item.node_flavor in nodeDistibution.nodeTypes) {
              nodeDistibution.nodeTypes[item.node_flavor].count += 1;
              if (item.node_type?.toLowerCase() === 'spot') {
                nodeDistibution.nodeTypes[item.node_flavor].spotCount += 1;
              }
            } else {
              nodeDistibution.nodeTypes[item.node_flavor] = {
                count: 1,
                spotCount: item.node_type?.toLowerCase() === 'spot' ? 1 : 0,
              };
            }
          });
          setNodeDistribution(nodeDistibution);
        })
        .catch((error) => {
          console.error(error);
        });
    }
  }, [accountId, showNodes]);
  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: largeVariant ? 'flex-start' : 'flex-end',
        gap: 'var(--ds-space-1)',
      }}
    >
      {largeVariant ? (
        <>
          <Box sx={{ display: 'flex', gap: 'var(--ds-space-6)', marginBottom: 'var(--ds-space-2)' }}>
            <ValueWithHeading
              updatedNode
              forCostSummary
              iconColor={ds.brand[500]}
              heading='On-Demand'
              icon={OnDemandIcon}
              value={node?.demand}
              hideLogo={largeVariant}
            />
            {!updatedNode && (
              <ValueWithHeading
                updatedNode
                forCostSummary
                icon={FallbackIcon}
                iconColor={ds.blue[500]}
                heading='Fallback'
                value={node?.fallback}
                hideLogo={largeVariant}
              />
            )}
            <ValueWithHeading
              updatedNode
              forCostSummary
              iconColor={ds.teal[400]}
              heading='Spot'
              icon={SpotIcon}
              value={node?.spot}
              hideLogo={largeVariant}
            />
          </Box>
          {clusterSummary && (
            <>
              <Box
                sx={{
                  display: 'flex',
                  overflow: 'hidden',
                  width: width ?? ds.space.mul(0, 115),
                  height: ds.space[1],
                  borderRadius: 'var(--ds-radius-xl)',
                }}
              >
                <Box
                  sx={{
                    height: '100%',
                    backgroundColor: updatedNode ? ds.blue[500] : ds.brand[500],
                    width: demandPercentage,
                  }}
                />
                <Box
                  sx={{
                    height: '100%',
                    backgroundColor: 'var(--ds-blue-500)',
                    width: fallbackPercentage,
                  }}
                />
                <Box
                  sx={{
                    height: '100%',
                    backgroundColor: updatedNode ? ds.blue[300] : ds.teal[400],
                    width: spotPercentage,
                  }}
                />
              </Box>
              <Divider />
              <NodeList nodeData={nodeDistribution} showNodes={showNodes} />
            </>
          )}
        </>
      ) : (
        <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-semibold)' }}>{total}</Typography>
      )}
      {'allClusterTable' == !sort && (
        <Box
          sx={{
            display: 'flex',
            overflow: 'hidden',
            width: largeVariant ? ds.space.mul(0, 115) : ds.space.mul(0, 50),
            height: largeVariant ? ds.space.mul(0, 4) : ds.space.mul(0, 3),
            borderRadius: largeVariant ? ds.radius.xl : ds.radius.sm,
          }}
        >
          <Box
            sx={{
              height: '100%',
              backgroundColor: 'var(--ds-brand-500)',
              width: demandPercentage,
            }}
          />
          <Box
            sx={{
              height: '100%',
              backgroundColor: 'var(--ds-brand-400)',
              width: fallbackPercentage,
            }}
          />
          <Box
            sx={{
              height: '100%',
              backgroundColor: 'var(--ds-teal-200)',
              width: spotPercentage,
            }}
          />
        </Box>
      )}
      {!largeVariant && (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'flex-end',
            color: 'var(--ds-brand-400)',
            gap: 'var(--ds-space-1)',
            fontSize: 'var(--ds-text-caption)',
            fontWeight: 'var(--ds-font-weight-semibold)',
          }}
        >
          <Tooltip title='on-demand'>
            <Box>
              <SafeIcon src={OnDemandIcon} alt={'On Demand icon'} />
              {demand}
            </Box>
          </Tooltip>
          <Tooltip title='fallback'>
            <Box>
              <SafeIcon src={FallbackIcon} alt={'Fall Back icon'} />
              {fallback}
            </Box>
          </Tooltip>
          <Tooltip title='spot'>
            <Box>
              <SafeIcon src={SpotIcon} alt={'Spot icon'} />
              {spot}
            </Box>
          </Tooltip>
        </Box>
      )}
    </Box>
  );
};

export default ClusterNode;

ClusterNode.propTypes = {
  node: PropTypes.any,
  sort: PropTypes.any,
  largeVariant: PropTypes.any,
  clusterSummary: PropTypes.bool,
  width: PropTypes.any,
  updatedNode: PropTypes.bool,
  accountId: PropTypes.string,
};
