import React from 'react';
import { EdgeLabelRenderer, BaseEdge, type EdgeProps } from 'reactflow';
import { Box } from '@mui/material';
import { Close, Add } from '@mui/icons-material';
import { Button } from '@components1/ds/Button';
import { useEdgeInteraction } from '@components1/workflow/hooks/useEdgeInteraction';

interface DeletableEdgeProps extends EdgeProps {
  onAddOnEdge?: (edgeId: string) => void;
}

const DeletableEdge: React.FC<DeletableEdgeProps> = ({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  style = {},
  markerEnd,
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

  return (
    <>
      <BaseEdge id={id} path={edgePath} style={style} markerEnd={markerEnd} />
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
        {isHovered && isEditorMode && (
          <Box
            style={{
              position: 'absolute',
              transform: `translate(-50%, -50%) translate(${labelX}px,${labelY}px)`,
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

export default DeletableEdge;
