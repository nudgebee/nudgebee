import React from 'react';
import { EdgeLabelRenderer, BaseEdge, type EdgeProps } from 'reactflow';
import { IconButton, Box } from '@mui/material';
import { Close, Add } from '@mui/icons-material';
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

export default DeletableEdge;
