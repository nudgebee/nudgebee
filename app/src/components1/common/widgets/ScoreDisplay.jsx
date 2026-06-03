import { useState } from 'react';
import { styled } from '@mui/material/styles';
import LinearProgress, { linearProgressClasses } from '@mui/material/LinearProgress';
import { Box, Typography, Popover } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import PropTypes from 'prop-types';

const ScoreProgress = styled(LinearProgress)(({ theme }) => ({
  width: '50px',
  height: '6px',
  borderRadius: 'var(--ds-radius-sm)',
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
    return <Typography sx={{ color: 'var(--ds-gray-400)', fontSize: 'var(--ds-text-small)', textAlign: 'center' }}>-</Typography>;
  }

  return (
    <>
      <Box
        onMouseEnter={handleMouseEnter}
        onMouseLeave={handleMouseLeave}
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--ds-space-1)',
          cursor: 'default',
        }}
      >
        <Typography sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-semibold)', color: getScoreColor() }}>
          {score}
        </Typography>
        <ScoreProgress
          variant='determinate'
          value={score}
          sx={{
            '& .MuiLinearProgress-bar': {
              backgroundColor: getScoreColor(),
            },
          }}
        />
        <InfoOutlinedIcon sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-brand-300)' }} />
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
            borderRadius: 'var(--ds-radius-lg)',
            boxShadow: '0px 4px 20px rgba(0, 0, 0, 0.15)',
            minWidth: '280px',
          },
        }}
        disableRestoreFocus
      >
        <Box sx={{ p: 2, minWidth: '240px' }}>
          <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: 2 }}>
            <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-brand-500)' }}>
              Priority Score
            </Typography>
          </Box>

          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
            {/* Key Factors */}
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>Severity</Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-brand-500)' }}>
                {factors.base_severity >= 50 ? 'High' : factors.base_severity >= 25 ? 'Medium' : 'Low'}
              </Typography>
            </Box>

            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>Service Tier</Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-brand-500)' }}>
                {getTierName(factors.service_tier)}
              </Typography>
            </Box>

            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>Environment</Typography>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-small)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  color: factors.env_multiplier < 1 ? '#9CA3AF' : '#374151',
                }}
              >
                {factors.env_multiplier === 1 ? 'Production' : factors.env_multiplier === 0.3 ? 'Non-Production' : 'Default'}
              </Typography>
            </Box>

            {factors.duplicate_penalty > 0 && (
              <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>Duplicate</Typography>
                <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-red-500)' }}>
                  Yes (score reduced)
                </Typography>
              </Box>
            )}

            {/* Confidence */}
            {(() => {
              const confidenceValue = parseFloat(actualConfidence) || 0;
              const confidencePercent = Math.round(confidenceValue * 100);
              return (
                <Box
                  sx={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center',
                    mt: 1,
                    pt: 1,
                    borderTop: '1px solid var(--ds-brand-150)',
                  }}
                >
                  <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>Confidence</Typography>
                  <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-brand-500)' }}>
                    {confidencePercent}%
                  </Typography>
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
