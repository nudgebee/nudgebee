import React from 'react';
import { EdgeLabelRenderer, BaseEdge, type EdgeProps } from 'reactflow';
import { Box, Typography } from '@mui/material';
import { Close, Add } from '@mui/icons-material';
import { Button } from '@components1/ds/Button';
import type { ParsedCondition } from '@components1/workflow/utils/conditionParser';
import { useEdgeInteraction } from '@components1/workflow/hooks/useEdgeInteraction';

export interface ConditionalEdgeData extends ParsedCondition {
  condition?: string;
}

interface ConditionalEdgeProps extends EdgeProps<ConditionalEdgeData> {
  onAddOnEdge?: (edgeId: string) => void;
}

const ConditionalEdge: React.FC<ConditionalEdgeProps> = ({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  style = {},
  markerEnd,
  data,
  onAddOnEdge,
}) => {
  const { edgePath, labelX, labelY, isHovered, isEditorMode, onEdgeClick, hoverHandlers } = useEdgeInteraction({
    id,
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
  });

  const edgeColor = data?.color || style?.stroke || 'rgb(192, 192, 192)';
  const label = data?.label;

  return (
    <>
      <BaseEdge
        id={id}
        path={edgePath}
        style={{
          ...style,
          stroke: edgeColor,
          strokeWidth: 2,
        }}
        markerEnd={markerEnd}
      />
      {/* Invisible wider path for better hover detection */}
      <path
        id={`${id}-hover`}
        d={edgePath}
        fill='none'
        stroke='transparent'
        strokeWidth={20}
        className='react-flow__edge-interaction'
        onMouseEnter={hoverHandlers.onMouseEnter}
        onMouseLeave={hoverHandlers.onMouseLeave}
        style={{ cursor: 'pointer' }}
      />
      <EdgeLabelRenderer>
        {/* Condition label */}
        {label && (
          <Box
            style={{
              position: 'absolute',
              transform: `translate(-50%, -50%) translate(${labelX}px,${labelY - 12}px)`,
              pointerEvents: 'none',
              zIndex: 999,
            }}
          >
            <Typography
              sx={{
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-medium)',
                color: edgeColor,
                backgroundColor: 'rgba(255, 255, 255, 0.95)',
                padding: 'var(--ds-space-1) var(--ds-space-1)',
                borderRadius: 'var(--ds-radius-sm)',
                border: `1px solid ${edgeColor}`,
                whiteSpace: 'nowrap',
                boxShadow: '0 1px 2px rgba(0,0,0,0.1)',
              }}
            >
              {label}
            </Typography>
          </Box>
        )}
        {/* Add and Delete buttons on hover */}
        {isHovered && isEditorMode && (
          <Box
            style={{
              position: 'absolute',
              transform: `translate(-50%, -50%) translate(${labelX}px,${labelY + 12}px)`,
              fontSize: 12,
              pointerEvents: 'all',
              zIndex: 1000,
              display: 'flex',
              gap: 'var(--ds-space-1)',
              alignItems: 'center',
            }}
            className='nodrag nopan'
            onMouseEnter={hoverHandlers.onMouseEnter}
            onMouseLeave={hoverHandlers.onMouseLeave}
          >
            {onAddOnEdge && (
              <Button
                composition='icon-only'
                tone='secondary'
                size='xs'
                aria-label='Add node on edge'
                icon={<Add sx={{ fontSize: 'var(--ds-text-body-lg)' }} />}
                onClick={(e) => {
                  e.stopPropagation();
                  onAddOnEdge(id);
                }}
              />
            )}
            <Button
              composition='icon-only'
              tone='danger'
              size='xs'
              aria-label='Delete edge'
              icon={<Close fontSize='small' />}
              onClick={onEdgeClick}
            />
          </Box>
        )}
      </EdgeLabelRenderer>
    </>
  );
};

export default ConditionalEdge;
