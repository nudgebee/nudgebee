import { useState } from 'react';
import { styled } from '@mui/material/styles';
import LinearProgress, { linearProgressClasses } from '@mui/material/LinearProgress';
import { Box, Typography, Popover } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import PropTypes from 'prop-types';

const ScoreProgress = styled(LinearProgress)(({ theme }) => ({
  width: '50px',
  height: '6px',
  borderRadius: '3px',
  [`&.${linearProgressClasses.colorPrimary}`]: {
    backgroundColor: theme.palette.grey[200],
  },
}));

const ScoreDisplay = ({ score, priority: _priority, scoreFactors, confidence }) => {
  const [anchorEl, setAnchorEl] = useState(null);
  const open = Boolean(anchorEl);

  const handleMouseEnter = (event) => {
    setAnchorEl(event.currentTarget);
  };

  const handleMouseLeave = () => {
    setAnchorEl(null);
  };

  // Get color based on score
  const getScoreColor = () => {
    if (score >= 75) {
      return '#EF4444';
    } // Red for high score (P0)
    if (score >= 50) {
      return '#F97316';
    } // Orange for medium-high (P1)
    if (score >= 25) {
      return '#EAB308';
    } // Yellow for medium (P2)
    return '#22C55E'; // Green for low (P3)
  };

  // Parse score factors if it's a string
  const factors = (() => {
    if (typeof scoreFactors === 'string') {
      try {
        return JSON.parse(scoreFactors || '{}');
      } catch {
        return {};
      }
    }
    return scoreFactors || {};
  })();

  // Get confidence from score_factors if available, otherwise use the prop
  const actualConfidence = factors.confidence !== undefined ? factors.confidence : confidence;

  // Get tier name
  const getTierName = (tier) => {
    switch (tier) {
      case 0:
        return 'Customer Facing';
      case 1:
        return 'Core Infra';
      case 2:
        return 'Business Service';
      case 3:
        return 'Monitoring';
      default:
        return `Tier ${tier}`;
    }
  };

  if (score === null || score === undefined) {
    return <Typography sx={{ color: '#9CA3AF', fontSize: '12px', textAlign: 'center' }}>-</Typography>;
  }

  return (
    <>
      <Box
        onMouseEnter={handleMouseEnter}
        onMouseLeave={handleMouseLeave}
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: '4px',
          cursor: 'default',
        }}
      >
        <Typography sx={{ fontSize: '13px', fontWeight: 600, color: getScoreColor() }}>{score}</Typography>
        <ScoreProgress
          variant='determinate'
          value={score}
          sx={{
            '& .MuiLinearProgress-bar': {
              backgroundColor: getScoreColor(),
            },
          }}
        />
        <InfoOutlinedIcon sx={{ fontSize: '12px', color: '#bcc0c7' }} />
      </Box>

      <Popover
        open={open}
        anchorEl={anchorEl}
        onClose={handleMouseLeave}
        anchorOrigin={{
          vertical: 'bottom',
          horizontal: 'center',
        }}
        transformOrigin={{
          vertical: 'top',
          horizontal: 'center',
        }}
        sx={{
          pointerEvents: 'none',
          '& .MuiPopover-paper': {
            borderRadius: '8px',
            boxShadow: '0px 4px 20px rgba(0, 0, 0, 0.15)',
            minWidth: '280px',
          },
        }}
        disableRestoreFocus
      >
        <Box sx={{ p: 2, minWidth: '240px' }}>
          <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
            <Typography sx={{ fontSize: '14px', fontWeight: 600, color: '#374151' }}>Priority Score</Typography>
          </Box>

          <Box sx={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
            {/* Key Factors */}
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <Typography sx={{ fontSize: '12px', color: '#6B7280' }}>Severity</Typography>
              <Typography sx={{ fontSize: '12px', fontWeight: 500, color: '#374151' }}>
                {factors.base_severity >= 50 ? 'High' : factors.base_severity >= 25 ? 'Medium' : 'Low'}
              </Typography>
            </Box>

            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <Typography sx={{ fontSize: '12px', color: '#6B7280' }}>Service Tier</Typography>
              <Typography sx={{ fontSize: '12px', fontWeight: 500, color: '#374151' }}>{getTierName(factors.service_tier)}</Typography>
            </Box>

            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <Typography sx={{ fontSize: '12px', color: '#6B7280' }}>Environment</Typography>
              <Typography sx={{ fontSize: '12px', fontWeight: 500, color: factors.env_multiplier < 1 ? '#9CA3AF' : '#374151' }}>
                {factors.env_multiplier === 1 ? 'Production' : factors.env_multiplier === 0.3 ? 'Non-Production' : 'Default'}
              </Typography>
            </Box>

            {factors.duplicate_penalty > 0 && (
              <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <Typography sx={{ fontSize: '12px', color: '#6B7280' }}>Duplicate</Typography>
                <Typography sx={{ fontSize: '12px', fontWeight: 500, color: '#EF4444' }}>Yes (score reduced)</Typography>
              </Box>
            )}

            {/* Confidence */}
            {(() => {
              const confidenceValue = parseFloat(actualConfidence) || 0;
              const confidencePercent = Math.round(confidenceValue * 100);
              return (
                <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mt: 1, pt: 1, borderTop: '1px solid #E5E7EB' }}>
                  <Typography sx={{ fontSize: '12px', color: '#6B7280' }}>Confidence</Typography>
                  <Typography sx={{ fontSize: '12px', fontWeight: 500, color: '#374151' }}>{confidencePercent}%</Typography>
                </Box>
              );
            })()}
          </Box>
        </Box>
      </Popover>
    </>
  );
};

ScoreDisplay.propTypes = {
  score: PropTypes.number,
  priority: PropTypes.string,
  scoreFactors: PropTypes.oneOfType([PropTypes.string, PropTypes.object]),
  confidence: PropTypes.number,
};

export default ScoreDisplay;
