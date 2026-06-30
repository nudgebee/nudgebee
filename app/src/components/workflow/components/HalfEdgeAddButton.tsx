import React from 'react';
import { Box, Typography } from '@mui/material';
import AddIcon from '@mui/icons-material/Add';
import { Handle, Position } from 'reactflow';

interface HalfEdgeAddButtonProps {
  onClick: () => void;
  style?: React.CSSProperties;
  sourceHandleId?: string;
  isConnectable?: boolean;
  id?: string;
}

const HalfEdgeAddButton: React.FC<HalfEdgeAddButtonProps> = ({ onClick, style, sourceHandleId, isConnectable = true, id }) => {
  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        ...style,
      }}
    >
      {/* Drag zone: dashed vertical line wraps a ReactFlow source Handle so users can pull an edge out.
          The Handle sits at the top of the line (= node's bottom edge) so the edge anchor doesn't jump
          when hasOutgoingEdge flips and this component unmounts in favor of the node's own Handle. */}
      <Box
        sx={{
          position: 'relative',
          width: '40px',
          height: '40px',
          display: 'flex',
          justifyContent: 'center',
          cursor: 'crosshair',
        }}
      >
        <svg width='2' height='40' style={{ overflow: 'visible', pointerEvents: 'none' }}>
          <line x1='1' y1='0' x2='1' y2='40' stroke='rgb(192, 192, 192)' strokeWidth='2' strokeDasharray='4 3' />
        </svg>
        {sourceHandleId && (
          <Handle
            type='source'
            position={Position.Bottom}
            id={sourceHandleId}
            isConnectable={isConnectable}
            style={{
              top: 0,
              left: '50%',
              transform: 'translateX(-50%)',
              width: '40px',
              height: '40px',
              borderRadius: '0',
              background: 'transparent',
              border: 'none',
              cursor: 'crosshair',
              zIndex: 1,
            }}
          />
        )}
      </Box>

      {/* Click zone: the pill. onClick-only, no Handle overlay. */}
      <Box
        id={id}
        className='nodrag nopan'
        onClick={(e) => {
          e.stopPropagation();
          onClick();
        }}
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--ds-space-1)',
          padding: 'var(--ds-space-1) var(--ds-space-2)',
          borderRadius: 'var(--ds-radius-xl)',
          backgroundColor: 'white',
          border: '1px dashed rgb(192, 192, 192)',
          transition: 'all 0.2s',
          mt: 'var(--ds-space-1)',
          cursor: 'pointer',
          '&:hover': {
            borderColor: 'var(--ds-blue-400)',
            backgroundColor: 'var(--ds-blue-100)',
            '& .add-icon': {
              color: 'var(--ds-blue-400)',
            },
            '& .add-text': {
              color: 'var(--ds-blue-400)',
            },
          },
        }}
      >
        <AddIcon className='add-icon' sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'rgb(156, 163, 175)', transition: 'color 0.2s' }} />
        <Typography
          className='add-text'
          sx={{
            fontSize: 'var(--ds-text-caption)',
            color: 'rgb(156, 163, 175)',
            fontWeight: 'var(--ds-font-weight-medium)',
            whiteSpace: 'nowrap',
            transition: 'color 0.2s',
          }}
        >
          Add Action
        </Typography>
      </Box>
    </Box>
  );
};

export default HalfEdgeAddButton;
