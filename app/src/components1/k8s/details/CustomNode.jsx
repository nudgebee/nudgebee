import React, { memo, useState, useRef, useEffect } from 'react';
import PropTypes from 'prop-types';
import { TableContainer, Table, Box, TableHead, TableRow, TableBody, TableCell, Typography } from '@mui/material';
import { Handle, Position, NodeToolbar } from 'reactflow';
import Text from '@common-new/format/Text';
import { formatBytes, formatLatencyInServiceMap } from 'src/utils/common';
import { ds } from 'src/utils/colors';
import SafeIcon from '@components1/common/SafeIcon';
import { IncomingIcon, OutgoingIcon } from '@assets';
const CustomNode = memo(({ data, selected }) => {
  const [isToolbarVisible, setIsToolbarVisible] = useState(false);
  const timeoutRef = useRef(null);

  const healthyInstances = data.entireNodeInstance?.DesiredInstances - (data?.entireNodeInstance?.FailedInstances || 0);
  const failedInstances = data.entireNodeInstance?.FailedInstances || 0;
  const totalInstances = data.entireNodeInstance?.DesiredInstances || 0;

  const instanceArray = [];
  for (let i = 0; i < healthyInstances; i++) {
    instanceArray.push('healthy');
  }
  for (let i = 0; i < failedInstances; i++) {
    instanceArray.push('failed');
  }

  const instanceUpstreams = data.entireNodeInstance?.Upstreams || [];
  const instanceDownstreams = data.entireNodeInstance?.Downstreams || [];

  // Filter upstreams to only include valid entries
  const filteredUpstreams = instanceUpstreams.filter(
    (n) => n.Id && n.Id.split(':').length >= 3 && n.Id.split(':')[2] && n.Id.split(':')[2].trim() !== ''
  );

  const hasToolbarContent =
    filteredUpstreams.length > 0 ||
    (instanceDownstreams && instanceDownstreams?.length > 0) ||
    data.entireNodeInstance.CPUThrottlingTime > 0 ||
    data.entireNodeInstance.VolumeUsed > 0 ||
    data.entireNodeInstance.VolumeSize > 0 ||
    data.entireNodeInstance.OOMKills > 0 ||
    data.entireNodeInstance.Restarts > 0;

  const handleMouseEnter = () => {
    if (timeoutRef.current) {
      clearTimeout(timeoutRef.current);
    }
    setIsToolbarVisible(true);
  };

  const handleMouseLeave = () => {
    // Add a small delay before hiding to allow user to move to tooltip
    timeoutRef.current = setTimeout(() => {
      setIsToolbarVisible(false);
    }, 200); // 200ms delay
  };

  useEffect(() => {
    return () => {
      if (timeoutRef.current) {
        clearTimeout(timeoutRef.current);
      }
    };
  }, []);

  return (
    <>
      <NodeToolbar isVisible={isToolbarVisible} position={'bottom'}>
        {hasToolbarContent ? (
          <Box
            sx={{
              borderRadius: 'var(--ds-radius-sm)',
              padding: 'var(--ds-space-3) var(--ds-space-2)',
              width: ds.space.mul(0, 175),
              maxHeight: ds.space.mul(0, 150),
              overflowY: 'auto',
              background: 'var(--ds-background-100)',
              boxShadow: 'rgba(255, 255, 255, 0.1) 0px 1px 1px 0px inset, rgba(50, 50, 93, 0.25) 0px 50px 100px -20px',
              '&::-webkit-scrollbar': {
                width: ds.space.mul(0, 3),
              },
              '&::-webkit-scrollbar-track': {
                backgroundColor: 'transparent',
              },
              '&::-webkit-scrollbar-thumb': {
                backgroundColor: 'var(--ds-brand-300)',
                borderRadius: 'var(--ds-radius-sm)',
                '&:hover': {
                  backgroundColor: 'var(--ds-gray-400)',
                },
              },
            }}
            onMouseEnter={handleMouseEnter}
            onMouseLeave={handleMouseLeave}
          >
            {/* Current Application Header */}
            <Box
              sx={{
                background: ds.blue[100],
                border: `1px solid ${ds.blue[300]}`,

                borderRadius: 'var(--ds-radius-sm)',
                padding: 'var(--ds-space-2) var(--ds-space-3)',
                color: ds.background[100],
              }}
            >
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-small)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  marginBottom: 'var(--ds-space-1)',
                  color: ds.brand[500],
                }}
              >
                <Text
                  value={data.entireNodeInstance?.Id?.name}
                  sx={{
                    color: ds.brand[500],
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                  }}
                />
              </Typography>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-small)',
                  color: ds.gray[600],
                }}
              >
                ns: {data.entireNodeInstance?.Id?.namespace}
              </Typography>
            </Box>

            {(data.entireNodeInstance.VolumeUsed > 0 || data.entireNodeInstance.VolumeSize > 0) && (
              <Box
                sx={{
                  border: `1px solid ${ds.blue[300]}`,
                  borderRadius: '0 0 var(--ds-radius-md) var(--ds-radius-md)',
                  padding: 'var(--ds-space-2) var(--ds-space-3)',
                  margin: '0px var(--ds-space-2) var(--ds-space-2) var(--ds-space-2)',
                  display: 'flex',
                  flexDirection: 'row',
                  gap: 'var(--ds-space-5)',
                  color: ds.background[100],
                }}
              >
                {data.entireNodeInstance.VolumeUsed > 0 && (
                  <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600] }}>
                    Volume Used
                    <Text
                      value={formatBytes(data.entireNodeInstance.VolumeUsed)}
                      sx={{ fontSize: 'var(--ds-text-small)', color: ds.brand[500], fontWeight: 'var(--ds-font-weight-medium)' }}
                    />
                  </Typography>
                )}

                {data.entireNodeInstance.VolumeSize > 0 && (
                  <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600] }}>
                    Volume Size
                    <Text
                      value={formatBytes(data.entireNodeInstance.VolumeSize)}
                      sx={{ fontSize: 'var(--ds-text-small)', color: ds.brand[500], fontWeight: 'var(--ds-font-weight-medium)' }}
                    />
                  </Typography>
                )}
              </Box>
            )}

            {/* System Metrics */}
            <Box sx={{ display: 'flex', flexDirection: 'row', gap: 'var(--ds-space-1)' }}>
              {data.entireNodeInstance.CPUThrottlingTime > 0 && (
                <Box
                  sx={{
                    background: `linear-gradient(95deg, ${ds.red[100]} 7.65%, ${ds.red[100]} 103.54%)`,
                    borderRadius: 'var(--ds-radius-sm)',
                    padding: 'var(--ds-space-1) var(--ds-space-2)',
                    width: 'fit-content',
                    marginTop: 'var(--ds-space-2)',
                  }}
                >
                  <Typography sx={{ color: ds.red[500], fontSize: 'var(--ds-text-small)' }}>
                    CPU Throttling Time: {formatLatencyInServiceMap(data.entireNodeInstance.CPUThrottlingTime)}
                  </Typography>
                </Box>
              )}
              {data.entireNodeInstance.OOMKills > 0 && (
                <Box
                  sx={{
                    background: `linear-gradient(95deg, ${ds.red[100]} 7.65%, ${ds.red[100]} 103.54%)`,
                    borderRadius: 'var(--ds-radius-sm)',
                    padding: 'var(--ds-space-1) var(--ds-space-2)',
                    width: 'fit-content',
                    marginTop: 'var(--ds-space-2)',
                  }}
                >
                  <Typography sx={{ color: ds.red[500], fontSize: 'var(--ds-text-small)' }}>
                    Total OOM Kill: {data.entireNodeInstance.OOMKills}
                  </Typography>
                </Box>
              )}
              {data.entireNodeInstance.Restarts > 0 && (
                <Box
                  sx={{
                    background: `linear-gradient(95deg, ${ds.red[100]} 7.65%, ${ds.red[100]} 103.54%)`,
                    borderRadius: 'var(--ds-radius-sm)',
                    padding: 'var(--ds-space-1) var(--ds-space-2)',
                    width: 'fit-content',
                    marginTop: 'var(--ds-space-2)',
                  }}
                >
                  <Typography sx={{ color: ds.red[500], fontSize: 'var(--ds-text-small)' }}>
                    Total Restarts: {data.entireNodeInstance.Restarts}
                  </Typography>
                </Box>
              )}
            </Box>

            {/* Outgoing Connections (Upstreams - services this app calls) */}
            {filteredUpstreams.length > 0 && (
              <>
                <Box
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 'var(--ds-space-1)',
                    marginTop: 'var(--ds-space-3)',
                    paddingLeft: 'var(--ds-space-1)',
                    marginBottom: 'var(--ds-space-1)',
                  }}
                >
                  <Box
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      width: ds.space.mul(0, 10),
                      height: ds.space.mul(0, 10),
                      borderRadius: 'var(--ds-radius-sm)',
                      border: `1px solid ${ds.gray[300]}`,
                    }}
                  >
                    <SafeIcon src={OutgoingIcon} alt='outgoing' width={16} height={16} />
                  </Box>
                  <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.brand[500] }}>
                    Outgoing ({filteredUpstreams.length})
                  </Typography>
                </Box>
                <TableContainer
                  sx={{
                    width: 'auto',
                    borderRadius: 'var(--ds-radius-sm)',
                    overflow: 'hidden',
                    background: 'var(--ds-background-100)',
                    border: `1px solid ${ds.gray[300]}`,
                    marginTop: 'var(--ds-space-2)',
                    marginBottom: 'var(--ds-space-4)',
                  }}
                >
                  <Table>
                    <TableHead>
                      <TableRow sx={{ background: ds.blue[100] }}>
                        <TableCell
                          sx={{
                            width: '40%',
                            padding: 'var(--ds-space-1)',
                            paddingLeft: 'var(--ds-space-3)',
                          }}
                        >
                          <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-medium)' }}>Service</Typography>
                        </TableCell>
                        <TableCell
                          sx={{
                            width: '20%',
                            padding: 'var(--ds-space-1)',
                            textAlign: 'right',
                          }}
                        >
                          <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-regular)' }}>Latency</Typography>
                        </TableCell>
                        <TableCell
                          sx={{
                            width: '20%',
                            padding: 'var(--ds-space-1)',
                            textAlign: 'right',
                            paddingRight: 'var(--ds-space-3)',
                          }}
                        >
                          <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-regular)' }}>Req Count</Typography>
                        </TableCell>
                      </TableRow>
                    </TableHead>
                    <TableBody>
                      {filteredUpstreams.map((item) => (
                        <TableRow key={item.Id}>
                          <TableCell
                            sx={{
                              width: '40%',
                              padding: 'var(--ds-space-1)',
                              paddingLeft: 'var(--ds-space-3)',
                            }}
                          >
                            <Typography>
                              <Text
                                value={`${item.Id.split(':')[2]}`}
                                showAutoEllipsis
                                sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }}
                              />
                            </Typography>
                            <Typography
                              sx={{
                                color: 'var(--ds-gray-600)',
                                fontSize: 'var(--ds-text-caption)',
                              }}
                            >
                              ns: {item.Id.split(':')[0] || 'External'}
                            </Typography>
                          </TableCell>
                          <TableCell
                            sx={{
                              width: '20%',
                              padding: 'var(--ds-space-1)',
                              fontSize: 'var(--ds-text-small)',
                              textAlign: 'right',
                            }}
                          >
                            {formatLatencyInServiceMap(item.Latency)}
                          </TableCell>
                          <TableCell
                            sx={{
                              width: '20%',
                              padding: 'var(--ds-space-1)',
                              fontSize: 'var(--ds-text-small)',
                              textAlign: 'right',
                              paddingRight: 'var(--ds-space-3)',
                            }}
                          >
                            {item.RequestCount || '-'}
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </TableContainer>
              </>
            )}

            {/* Incoming Connections (Downstreams - services that call this app) */}
            {instanceDownstreams && instanceDownstreams?.length > 0 && (
              <>
                <Box
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 'var(--ds-space-1)',
                    marginTop: 'var(--ds-space-3)',
                    marginBottom: 'var(--ds-space-1)',
                    paddingLeft: 'var(--ds-space-1)',
                  }}
                >
                  <Box
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      width: ds.space.mul(0, 10),
                      height: ds.space.mul(0, 10),
                      borderRadius: 'var(--ds-radius-sm)',
                      border: `1px solid ${ds.gray[300]}`,
                    }}
                  >
                    <SafeIcon src={IncomingIcon} alt='incoming' width={16} height={16} />
                  </Box>
                  <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.brand[500] }}>
                    Incoming ({instanceDownstreams.length})
                  </Typography>
                </Box>
                <TableContainer
                  sx={{
                    width: 'auto',
                    borderRadius: 'var(--ds-radius-sm)',
                    overflow: 'hidden',
                    background: 'var(--ds-background-100)',
                    border: `1px solid ${ds.gray[300]}`,
                    marginTop: 'var(--ds-space-2)',
                  }}
                >
                  <Table>
                    <TableHead>
                      <TableRow sx={{ background: ds.blue[100] }}>
                        <TableCell
                          sx={{
                            width: '40%',
                            padding: 'var(--ds-space-1)',
                            paddingLeft: 'var(--ds-space-3)',
                          }}
                        >
                          <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-medium)' }}>Service</Typography>
                        </TableCell>
                        <TableCell
                          sx={{
                            width: '20%',
                            padding: 'var(--ds-space-1)',
                            textAlign: 'right',
                          }}
                        >
                          <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-regular)' }}>Latency</Typography>
                        </TableCell>
                        <TableCell
                          sx={{
                            width: '20%',
                            padding: 'var(--ds-space-1)',
                            textAlign: 'right',
                            paddingRight: 'var(--ds-space-3)',
                          }}
                        >
                          <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-regular)' }}>Req Count</Typography>
                        </TableCell>
                      </TableRow>
                    </TableHead>
                    <TableBody>
                      {instanceDownstreams.map((item, index) => (
                        <TableRow key={`${item.Id?.name}-${item.Id?.namespace}-${index}`}>
                          <TableCell
                            sx={{
                              width: '40%',
                              padding: 'var(--ds-space-1)',
                              fontSize: 'var(--ds-text-small)',
                              paddingLeft: 'var(--ds-space-3)',
                            }}
                          >
                            <Typography>
                              <Text
                                value={item.Id?.name || 'Unknown'}
                                showAutoEllipsis
                                sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }}
                              />
                            </Typography>
                            <Typography
                              sx={{
                                color: 'var(--ds-gray-600)',
                                fontSize: 'var(--ds-text-caption)',
                              }}
                            >
                              ns: {item.Id?.namespace || 'External'}
                            </Typography>
                          </TableCell>
                          <TableCell
                            sx={{
                              width: '20%',
                              padding: 'var(--ds-space-1)',
                              fontSize: 'var(--ds-text-small)',
                              textAlign: 'right',
                            }}
                          >
                            {item.Latency ? formatLatencyInServiceMap(item.Latency) : '-'}
                          </TableCell>
                          <TableCell
                            sx={{
                              width: '20%',
                              padding: 'var(--ds-space-1)',
                              fontSize: 'var(--ds-text-small)',
                              textAlign: 'right',
                              paddingRight: 'var(--ds-space-3)',
                            }}
                          >
                            {item.RequestCount || '-'}
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </TableContainer>
              </>
            )}
          </Box>
        ) : null}
      </NodeToolbar>

      <div
        style={{
          padding: 'var(--ds-space-1)',
          border: data.entireNodeInstance?.IsHealthy === false ? `2px solid ${ds.red[500]}` : 'none',
          borderRadius: 'var(--ds-radius-lg)',
          display: 'inline-block',
          boxShadow: selected ? '0 0 0 2px #2196f3' : 'none',
        }}
        onMouseEnter={handleMouseEnter}
        onMouseLeave={handleMouseLeave}
      >
        <div
          className='react-flow__node-default'
          style={{
            display: 'flex',
            flexDirection: 'column',
            justifyContent: 'center',
            alignItems: 'center',
            border: selected ? `2px solid ${ds.blue[500]}` : !data.changeColor ? `2px solid ${ds.blue[300]}` : `2px solid ${ds.green[300]}`,
            borderRadius: 'var(--ds-radius-md)',
            width: ds.space.mul(0, 100),
          }}
        >
          {data?.label}

          {totalInstances > 0 && (
            <Box
              sx={{
                display: 'flex',
                flexWrap: 'wrap',
                justifyContent: 'center',
                gap: 'var(--ds-space-1)',
                mt: 'var(--ds-space-1)',
                mb: 'var(--ds-space-1)',
                maxWidth: ds.space.mul(0, 90),
              }}
            >
              {instanceArray.map((type, index) => (
                <Box
                  key={index}
                  sx={{
                    width: ds.space.mul(0, 5),
                    height: ds.space.mul(0, 5),
                    backgroundColor: type === 'healthy' ? ds.green[300] : ds.red[400],
                    borderRadius: 'var(--ds-radius-sm)',
                  }}
                />
              ))}
            </Box>
          )}
        </div>
      </div>

      <Handle type='target' position={Position.Left} />
      <Handle type='source' position={Position.Right} />
    </>
  );
});

CustomNode.displayName = 'CustomNode';

CustomNode.propTypes = {
  data: PropTypes.any,
  selected: PropTypes.bool,
};

export default CustomNode;
