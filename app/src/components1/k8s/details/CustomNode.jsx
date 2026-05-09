import React, { memo, useState, useRef, useEffect } from 'react';
import PropTypes from 'prop-types';
import { TableContainer, Table, Box, TableHead, TableRow, TableBody, TableCell, Typography } from '@mui/material';
import { Handle, Position, NodeToolbar } from 'reactflow';
import Text from '@components1/common/format/Text';
import { formatBytes, formatLatencyInServiceMap } from 'src/utils/common';
import { colors } from 'src/utils/colors';
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
              borderRadius: '4px',
              padding: '12px 10px',
              width: '350px',
              maxHeight: '300px',
              overflowY: 'auto',
              background: '#ffffff',
              boxShadow: 'rgba(255, 255, 255, 0.1) 0px 1px 1px 0px inset, rgba(50, 50, 93, 0.25) 0px 50px 100px -20px',
              '&::-webkit-scrollbar': {
                width: '6px',
              },
              '&::-webkit-scrollbar-track': {
                backgroundColor: 'transparent',
              },
              '&::-webkit-scrollbar-thumb': {
                backgroundColor: '#C1C1C1',
                borderRadius: '4px',
                '&:hover': {
                  backgroundColor: '#A8A8A8',
                },
              },
            }}
            onMouseEnter={handleMouseEnter}
            onMouseLeave={handleMouseLeave}
          >
            {/* Current Application Header */}
            <Box
              sx={{
                background: colors.background.primaryLightest,
                border: `1px solid ${colors.border.primaryLight}`,

                borderRadius: '4px',
                padding: '8px 12px',
                color: 'white',
              }}
            >
              <Typography
                sx={{
                  fontSize: '12px',
                  fontWeight: 500,
                  marginBottom: '1px',
                  color: colors.text.secondary,
                }}
              >
                <Text
                  value={data.entireNodeInstance?.Id?.name}
                  sx={{
                    color: colors.text.secondary,
                    fontSize: '14px',
                    fontWeight: 500,
                  }}
                />
              </Typography>
              <Typography
                sx={{
                  fontSize: '12px',
                  color: colors.text.tertiary,
                }}
              >
                ns: {data.entireNodeInstance?.Id?.namespace}
              </Typography>
            </Box>

            {(data.entireNodeInstance.VolumeUsed > 0 || data.entireNodeInstance.VolumeSize > 0) && (
              <Box
                sx={{
                  border: `1px solid ${colors.border.primaryLight}`,
                  borderRadius: '0 0 6px 6px',
                  padding: '8px 12px',
                  margin: '0px 8px 8px 8px',
                  display: 'flex',
                  flexDirection: 'row',
                  gap: '24px',
                  color: 'white',
                }}
              >
                {data.entireNodeInstance.VolumeUsed > 0 && (
                  <Typography sx={{ fontSize: '12px', color: colors.text.tertiary }}>
                    Volume Used
                    <Text
                      value={formatBytes(data.entireNodeInstance.VolumeUsed)}
                      sx={{ fontSize: '12px', color: colors.text.secondary, fontWeight: 500 }}
                    />
                  </Typography>
                )}

                {data.entireNodeInstance.VolumeSize > 0 && (
                  <Typography sx={{ fontSize: '12px', color: colors.text.tertiary }}>
                    Volume Size
                    <Text
                      value={formatBytes(data.entireNodeInstance.VolumeSize)}
                      sx={{ fontSize: '12px', color: colors.text.secondary, fontWeight: 500 }}
                    />
                  </Typography>
                )}
              </Box>
            )}

            {/* System Metrics */}
            <Box sx={{ display: 'flex', flexDirection: 'row', gap: '4px' }}>
              {data.entireNodeInstance.CPUThrottlingTime > 0 && (
                <Box
                  sx={{
                    background: colors.background.lightRedLabel,
                    borderRadius: '4px',
                    padding: '4px 8px',
                    width: 'fit-content',
                    marginTop: '8px',
                  }}
                >
                  <Typography sx={{ color: 'red', fontSize: '12px' }}>
                    CPU Throttling Time: {formatLatencyInServiceMap(data.entireNodeInstance.CPUThrottlingTime)}
                  </Typography>
                </Box>
              )}
              {data.entireNodeInstance.OOMKills > 0 && (
                <Box
                  sx={{
                    background: colors.background.lightRedLabel,
                    borderRadius: '4px',
                    padding: '4px 8px',
                    width: 'fit-content',
                    marginTop: '8px',
                  }}
                >
                  <Typography sx={{ color: 'red', fontSize: '12px' }}>Total OOM Kill: {data.entireNodeInstance.OOMKills}</Typography>
                </Box>
              )}
              {data.entireNodeInstance.Restarts > 0 && (
                <Box
                  sx={{
                    background: colors.background.lightRedLabel,
                    borderRadius: '4px',
                    padding: '4px 8px',
                    width: 'fit-content',
                    marginTop: '8px',
                  }}
                >
                  <Typography sx={{ color: 'red', fontSize: '12px' }}>Total Restarts: {data.entireNodeInstance.Restarts}</Typography>
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
                    gap: '6px',
                    marginTop: '12px',
                    paddingLeft: '4px',
                    marginBottom: '4px',
                  }}
                >
                  <Box
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      width: '20px',
                      height: '20px',
                      borderRadius: '4px',
                      border: '1px solid rgb(219, 219, 219)',
                    }}
                  >
                    <SafeIcon src={OutgoingIcon} alt='outgoing' width={16} height={16} />
                  </Box>
                  <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.secondary }}>
                    Outgoing ({filteredUpstreams.length})
                  </Typography>
                </Box>
                <TableContainer
                  sx={{
                    width: 'auto',
                    borderRadius: '4px',
                    overflow: 'hidden',
                    background: '#ffffff',
                    border: '1px solid rgb(219, 219, 219)',
                    marginTop: '8px',
                    marginBottom: '20px',
                  }}
                >
                  <Table>
                    <TableHead>
                      <TableRow sx={{ background: colors.background.primaryLightest }}>
                        <TableCell
                          sx={{
                            width: '40%',
                            padding: '4px',
                            paddingLeft: '12px',
                          }}
                        >
                          <Typography sx={{ fontSize: '11px', fontWeight: 500 }}>Service</Typography>
                        </TableCell>
                        <TableCell
                          sx={{
                            width: '20%',
                            padding: '4px',
                            textAlign: 'right',
                          }}
                        >
                          <Typography sx={{ fontSize: '11px', fontWeight: 400 }}>Latency</Typography>
                        </TableCell>
                        <TableCell
                          sx={{
                            width: '20%',
                            padding: '4px',
                            textAlign: 'right',
                            paddingRight: '12px',
                          }}
                        >
                          <Typography sx={{ fontSize: '11px', fontWeight: 400 }}>Req Count</Typography>
                        </TableCell>
                      </TableRow>
                    </TableHead>
                    <TableBody>
                      {filteredUpstreams.map((item) => (
                        <TableRow key={item.Id}>
                          <TableCell
                            sx={{
                              width: '40%',
                              padding: '4px',
                              paddingLeft: '12px',
                            }}
                          >
                            <Typography>
                              <Text value={`${item.Id.split(':')[2]}`} showAutoEllipsis sx={{ fontSize: '12px', fontWeight: 400 }} />
                            </Typography>
                            <Typography
                              sx={{
                                color: '#737373',
                                fontSize: '11px',
                              }}
                            >
                              ns: {item.Id.split(':')[0] || 'External'}
                            </Typography>
                          </TableCell>
                          <TableCell
                            sx={{
                              width: '20%',
                              padding: '4px',
                              fontSize: '12px',
                              textAlign: 'right',
                            }}
                          >
                            {formatLatencyInServiceMap(item.Latency)}
                          </TableCell>
                          <TableCell
                            sx={{
                              width: '20%',
                              padding: '4px',
                              fontSize: '12px',
                              textAlign: 'right',
                              paddingRight: '12px',
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
                    gap: '6px',
                    marginTop: '12px',
                    marginBottom: '4px',
                    paddingLeft: '4px',
                  }}
                >
                  <Box
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'center',
                      width: '20px',
                      height: '20px',
                      borderRadius: '4px',
                      border: '1px solid rgb(219, 219, 219)',
                    }}
                  >
                    <SafeIcon src={IncomingIcon} alt='incoming' width={16} height={16} />
                  </Box>
                  <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.secondary }}>
                    Incoming ({instanceDownstreams.length})
                  </Typography>
                </Box>
                <TableContainer
                  sx={{
                    width: 'auto',
                    borderRadius: '4px',
                    overflow: 'hidden',
                    background: '#ffffff',
                    border: '1px solid rgb(219, 219, 219)',
                    marginTop: '8px',
                  }}
                >
                  <Table>
                    <TableHead>
                      <TableRow sx={{ background: colors.background.primaryLightest }}>
                        <TableCell
                          sx={{
                            width: '40%',
                            padding: '4px',
                            paddingLeft: '12px',
                          }}
                        >
                          <Typography sx={{ fontSize: '11px', fontWeight: 500 }}>Service</Typography>
                        </TableCell>
                        <TableCell
                          sx={{
                            width: '20%',
                            padding: '4px',
                            textAlign: 'right',
                          }}
                        >
                          <Typography sx={{ fontSize: '11px', fontWeight: 400 }}>Latency</Typography>
                        </TableCell>
                        <TableCell
                          sx={{
                            width: '20%',
                            padding: '4px',
                            textAlign: 'right',
                            paddingRight: '12px',
                          }}
                        >
                          <Typography sx={{ fontSize: '11px', fontWeight: 400 }}>Req Count</Typography>
                        </TableCell>
                      </TableRow>
                    </TableHead>
                    <TableBody>
                      {instanceDownstreams.map((item, index) => (
                        <TableRow key={`${item.Id?.name}-${item.Id?.namespace}-${index}`}>
                          <TableCell
                            sx={{
                              width: '40%',
                              padding: '4px',
                              fontSize: '12px',
                              paddingLeft: '12px',
                            }}
                          >
                            <Typography>
                              <Text value={item.Id?.name || 'Unknown'} showAutoEllipsis sx={{ fontSize: '12px', fontWeight: 400 }} />
                            </Typography>
                            <Typography
                              sx={{
                                color: '#737373',
                                fontSize: '11px',
                              }}
                            >
                              ns: {item.Id?.namespace || 'External'}
                            </Typography>
                          </TableCell>
                          <TableCell
                            sx={{
                              width: '20%',
                              padding: '4px',
                              fontSize: '12px',
                              textAlign: 'right',
                            }}
                          >
                            {item.Latency ? formatLatencyInServiceMap(item.Latency) : '-'}
                          </TableCell>
                          <TableCell
                            sx={{
                              width: '20%',
                              padding: '4px',
                              fontSize: '12px',
                              textAlign: 'right',
                              paddingRight: '12px',
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
          padding: '2px',
          border: data.entireNodeInstance?.IsHealthy === false ? '2px solid red' : 'none',
          borderRadius: '9px',
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
            border: selected ? '2px solid #2196f3' : !data.changeColor ? '2px solid #96c2fd' : '2px solid #2BDCA2',
            borderRadius: '7px',
            width: '200px',
          }}
        >
          {data?.label}

          {totalInstances > 0 && (
            <Box
              sx={{
                display: 'flex',
                flexWrap: 'wrap',
                justifyContent: 'center',
                gap: '2px',
                mt: '4px',
                mb: '4px',
                maxWidth: '180px',
              }}
            >
              {instanceArray.map((type, index) => (
                <Box
                  key={index}
                  sx={{
                    width: '10px',
                    height: '10px',
                    backgroundColor: type === 'healthy' ? '#2BDCA2' : '#FF4D4F',
                    borderRadius: '2px',
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
