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
          gap: '4px',
          padding: '4px 10px',
          borderRadius: '16px',
          backgroundColor: 'white',
          border: '1px dashed rgb(192, 192, 192)',
          transition: 'all 0.2s',
          mt: '2px',
          cursor: 'pointer',
          '&:hover': {
            borderColor: '#6172F3',
            backgroundColor: '#f0f4ff',
            '& .add-icon': {
              color: '#6172F3',
            },
            '& .add-text': {
              color: '#6172F3',
            },
          },
        }}
      >
        <AddIcon className='add-icon' sx={{ fontSize: '14px', color: 'rgb(156, 163, 175)', transition: 'color 0.2s' }} />
        <Typography
          className='add-text'
          sx={{ fontSize: '11px', color: 'rgb(156, 163, 175)', fontWeight: 500, whiteSpace: 'nowrap', transition: 'color 0.2s' }}
        >
          Add Action
        </Typography>
      </Box>
    </Box>
  );
};

export default HalfEdgeAddButton;
