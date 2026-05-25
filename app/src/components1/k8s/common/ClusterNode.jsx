import { Tooltip, Box, Typography, Divider, Grid } from '@mui/material';
import React, { useEffect, useState } from 'react';
import OnDemandIcon from '@assets/on-demand-icon.svg';
import FallbackIcon from '@assets/fallback-icon.svg';
import SpotIcon from '@assets/spot-icon.svg';
import ValueWithHeading from './ValueWithHeading';
import SafeIcon from '@components1/common/SafeIcon';
import PropTypes from 'prop-types';
import { Modal } from '@components1/common/modal';
import CustomTable from '@components1/common/tables/CustomTable2';
import k8sApi from '@api1/kubernetes';

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
          <Box display={'flex'} alignItems={'center'} gap={'10px'} fontSize={'17px'} fontWeight={600} color='#374151'>
            Instances
          </Box>
        }
        contentStyles={{
          padding: '24px 40px',
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
      <Box sx={{ width: '100%', mt: '10px' }}>
        <Box sx={{ display: 'grid', gridTemplateColumns: '1fr', gap: '5px' }}>
          {showNodes &&
            Object.entries(nodeData?.nodeTypes ?? {}).map(([key, value]) => {
              return (
                <Grid container key={key} justifyContent='space-between'>
                  <Grid item>
                    <Typography sx={{ fontSize: '11px', fontWeight: '400', color: '#9F9F9F' }}>{key}</Typography>
                  </Grid>
                  <Grid item>
                    <Typography sx={{ fontSize: '11px', fontWeight: '500', color: '#374151' }}>
                      {value.count} <span style={{ color: '#9F9F9F', fontWeight: '400' }}>({value.spotCount} spot)</span>
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
        gap: '4px',
      }}
    >
      {largeVariant ? (
        <>
          <Box sx={{ display: 'flex', gap: '32px', marginBottom: '8px' }}>
            <ValueWithHeading
              updatedNode
              forCostSummary
              iconColor='#274175'
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
                iconColor='#4772C6'
                heading='Fallback'
                value={node?.fallback}
                hideLogo={largeVariant}
              />
            )}
            <ValueWithHeading
              updatedNode
              forCostSummary
              iconColor='#44CBE9'
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
                  width: width ?? '230px',
                  height: '4px',
                  borderRadius: '14px',
                }}
              >
                <Box
                  sx={{
                    height: '100%',
                    backgroundColor: updatedNode ? '#3B82F6' : '#274175',
                    width: demandPercentage,
                  }}
                />
                <Box
                  sx={{
                    height: '100%',
                    backgroundColor: '#4772C6',
                    width: fallbackPercentage,
                  }}
                />
                <Box
                  sx={{
                    height: '100%',
                    backgroundColor: updatedNode ? '#BFDBFE' : '#44CBE9',
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
        <Typography sx={{ fontSize: '14px', fontWeight: 600 }}>{total}</Typography>
      )}
      {'allClusterTable' == !sort && (
        <Box
          sx={{
            display: 'flex',
            overflow: 'hidden',
            width: largeVariant ? '230px' : '100px',
            height: largeVariant ? '9px' : '6px',
            borderRadius: largeVariant ? '14px' : '2px',
          }}
        >
          <Box
            sx={{
              height: '100%',
              backgroundColor: '#2F4267',
              width: demandPercentage,
            }}
          />
          <Box
            sx={{
              height: '100%',
              backgroundColor: '#758FC3',
              width: fallbackPercentage,
            }}
          />
          <Box
            sx={{
              height: '100%',
              backgroundColor: '#A8E3F0',
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
            color: '#768BB2',
            gap: '4px',
            fontSize: '10px',
            fontWeight: 600,
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
