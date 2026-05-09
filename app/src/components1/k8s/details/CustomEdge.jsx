import React from 'react';
import { BaseEdge, EdgeLabelRenderer, getBezierPath } from 'reactflow';
import { formatBytes } from 'src/utils/common';
import { colors } from 'src/utils/colors';

const CustomEdge = ({ _id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, style = {}, data, markerEnd, selected, animated }) => {
  // Using bezier path for smoother curves between nodes instead of step path
  const [edgePath, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
    curvature: 0.4,
  });

  const isFailure = data?.FailureCount > 0;
  const isHighlighted = data?.isHighlighted;

  let edgeColor = colors.text.greyDark;
  let strokeWidth = 1.5;
  let opacity = 0.75;

  if (isFailure) {
    edgeColor = colors.error;
    strokeWidth = 2;
    opacity = 0.9;
  }
  if (isHighlighted) {
    edgeColor = colors.primary;
    strokeWidth = 2.5;
    opacity = 1;
  }

  if (selected) {
    strokeWidth += 1;
    opacity = 1;
  }

  const networkMetrics = {
    rps: data?.RequestCount || 0,
    receivedBps: data?.BytesReceived || 0,
    transferredBps: data?.BytesSent || 0,
  };

  return (
    <>
      <BaseEdge
        path={edgePath}
        markerEnd={markerEnd}
        style={{
          ...style,
          stroke: edgeColor,
          strokeWidth: strokeWidth,
          opacity: opacity,
          strokeDasharray: animated ? '5,5' : 'none',
          animation: animated ? 'flowAnimation 30s linear infinite' : 'none',
        }}
      />

      {data?.label && (
        <EdgeLabelRenderer>
          <div
            style={{
              position: 'absolute',
              transform: `translate(-50%, -50%) translate(${labelX}px,${labelY}px)`,
              background: colors.background.white,
              padding: '3px 8px',
              borderRadius: '4px',
              fontSize: '11px',
              fontWeight: 'bold',
              color: isFailure ? colors.error : colors.text.secondary,
              border: `1px solid ${isFailure ? colors.error : colors.border.secondaryLightest}`,
              opacity: isHighlighted ? 1 : 0.9,
              boxShadow: '0 1px 3px rgba(0,0,0,0.1)',
              pointerEvents: 'all',
              zIndex: 1000,
              whiteSpace: 'nowrap',
            }}
            className='nodrag nopan'
          >
            {data.label}
            {isFailure && data.failureCount > 0 && (
              <span style={{ marginLeft: '10px', color: colors.error, fontWeight: 'bold' }}>({data.failureCount})</span>
            )}
          </div>
        </EdgeLabelRenderer>
      )}

      {isHighlighted && (networkMetrics.rps > 0 || networkMetrics.receivedBps || networkMetrics.transferredBps) && (
        <EdgeLabelRenderer>
          <div
            style={{
              position: 'absolute',
              transform: `translate(-50%, -50%) translate(${labelX}px,${labelY + 40}px)`,
              background: colors.background.white,
              padding: '6px 10px',
              borderRadius: '4px',
              fontSize: '11px',
              color: colors.text.secondary,
              border: `1px solid ${edgeColor}`,
              opacity: 1,
              boxShadow: '0 2px 6px rgba(0,0,0,0.15)',
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'center',
              zIndex: 1001,
            }}
            className='nodrag nopan'
          >
            {networkMetrics.rps > 0 && (
              <div style={{ display: 'flex', alignItems: 'center', marginBottom: '2px' }}>
                <span style={{ fontWeight: 'bold' }}>{networkMetrics.rps} req</span>
              </div>
            )}
            <div>
              {networkMetrics.receivedBps > 0 && (
                <span style={{ color: colors.success, marginRight: '4px' }}>
                  ↑{formatBytes(networkMetrics.receivedBps, true, '/sec')?.toLowerCase()}
                </span>
              )}
              {networkMetrics.transferredBps > 0 && (
                <span style={{ color: colors.success }}>↓{formatBytes(networkMetrics.transferredBps, true, '/sec')?.toLowerCase()}</span>
              )}
            </div>
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  );
};

export default CustomEdge;
