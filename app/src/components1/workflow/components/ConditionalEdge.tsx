import React from 'react';
import { EdgeLabelRenderer, BaseEdge, type EdgeProps } from 'reactflow';
import { IconButton, Box, Typography } from '@mui/material';
import { Close, Add } from '@mui/icons-material';
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
                fontSize: '10px',
                fontWeight: 500,
                color: edgeColor,
                backgroundColor: 'rgba(255, 255, 255, 0.95)',
                padding: '2px 6px',
                borderRadius: '4px',
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
              gap: '6px',
              alignItems: 'center',
            }}
            className='nodrag nopan'
            onMouseEnter={hoverHandlers.onMouseEnter}
            onMouseLeave={hoverHandlers.onMouseLeave}
          >
            {onAddOnEdge && (
              <IconButton
                onClick={(e) => {
                  e.stopPropagation();
                  onAddOnEdge(id);
                }}
                size='small'
                sx={{
                  backgroundColor: 'white',
                  color: '#6b7280',
                  width: '20px',
                  height: '20px',
                  border: '1px solid #e5e7eb',
                  opacity: 0.9,
                  transition: 'all 0.2s',
                  '&:hover': {
                    backgroundColor: '#f0f4ff',
                    borderColor: '#6172F3',
                    color: '#6172F3',
                    opacity: 1,
                  },
                }}
              >
                <Add sx={{ fontSize: '14px' }} />
              </IconButton>
            )}
            <IconButton
              onClick={onEdgeClick}
              size='small'
              sx={{
                backgroundColor: '#ff4444',
                color: 'white',
                width: '20px',
                height: '20px',
                opacity: 0.9,
                transition: 'all 0.2s',
                '&:hover': {
                  backgroundColor: '#cc3333',
                  opacity: 1,
                },
              }}
            >
              <Close fontSize='small' />
            </IconButton>
          </Box>
        )}
      </EdgeLabelRenderer>
    </>
  );
};

export default ConditionalEdge;
