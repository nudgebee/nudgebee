import { useMemo, useState } from 'react';
import { styled } from '@mui/material/styles';
import LinearProgress, { linearProgressClasses } from '@mui/material/LinearProgress';
import { Box, Typography, Popover } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import PropTypes from 'prop-types';
import { ds } from 'src/utils/colors';

const ScoreProgress = styled(LinearProgress)(() => ({
  width: '50px',
  height: '6px',
  borderRadius: ds.radius.sm,
  [`&.${linearProgressClasses.colorPrimary}`]: {
    backgroundColor: ds.gray[200],
  },
}));

// Score → tone mapping. Red >= 75 (P0), amber >= 50 (P1), yellow >= 25 (P2), green low (P3).
// Yellow (P2) uses ds.amber as well — ds.yellow is reserved for brand/focus per ds tokens.
const getScoreColor = (score) => {
  if (score >= 75) return ds.red[500];
  if (score >= 50) return ds.amber[500];
  if (score >= 25) return ds.amber[400];
  return ds.green[500];
};

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

const ScoreDisplay = ({ score, priority: _priority, scoreFactors, confidence }) => {
  const [anchorEl, setAnchorEl] = useState(null);
  const open = Boolean(anchorEl);

  const handleMouseEnter = (event) => setAnchorEl(event.currentTarget);
  const handleMouseLeave = () => setAnchorEl(null);

  const factors = useMemo(() => {
    if (typeof scoreFactors === 'string') {
      try {
        return JSON.parse(scoreFactors || '{}');
      } catch {
        return {};
      }
    }
    return scoreFactors || {};
  }, [scoreFactors]);

  const actualConfidence = factors.confidence !== undefined ? factors.confidence : confidence;

  if (score === null || score === undefined) {
    return <Typography sx={{ color: ds.gray[400], fontSize: ds.text.small, textAlign: 'center' }}>-</Typography>;
  }

  const scoreColor = getScoreColor(score);

  return (
    <>
      <Box
        data-testid='score-display'
        onMouseEnter={handleMouseEnter}
        onMouseLeave={handleMouseLeave}
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: ds.space[1],
          cursor: 'default',
        }}
      >
        <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: scoreColor }}>{score}</Typography>
        <ScoreProgress
          variant='determinate'
          value={score}
          sx={{
            '& .MuiLinearProgress-bar': {
              backgroundColor: scoreColor,
            },
          }}
        />
        <InfoOutlinedIcon sx={{ fontSize: ds.text.small, color: ds.gray[400] }} />
      </Box>

      <Popover
        open={open}
        anchorEl={anchorEl}
        onClose={handleMouseLeave}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'center' }}
        transformOrigin={{ vertical: 'top', horizontal: 'center' }}
        sx={{
          pointerEvents: 'none',
          '& .MuiPopover-paper': {
            borderRadius: ds.radius.md,
            boxShadow: `0 4px 20px ${ds.gray.alpha[300]}`,
            minWidth: '280px',
          },
        }}
        disableRestoreFocus
      >
        <Box sx={{ p: ds.space[4], minWidth: '240px' }}>
          <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: ds.space[4] }}>
            <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>Priority Score</Typography>
          </Box>

          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>Severity</Typography>
              <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.gray[700] }}>
                {factors.base_severity >= 50 ? 'High' : factors.base_severity >= 25 ? 'Medium' : 'Low'}
              </Typography>
            </Box>

            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>Service Tier</Typography>
              <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.gray[700] }}>
                {getTierName(factors.service_tier)}
              </Typography>
            </Box>

            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>Environment</Typography>
              <Typography
                sx={{
                  fontSize: ds.text.small,
                  fontWeight: ds.weight.medium,
                  color: factors.env_multiplier < 1 ? ds.gray[400] : ds.gray[700],
                }}
              >
                {factors.env_multiplier === 1 ? 'Production' : factors.env_multiplier === 0.3 ? 'Non-Production' : 'Default'}
              </Typography>
            </Box>

            {factors.duplicate_penalty > 0 && (
              <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>Duplicate</Typography>
                <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.red[500] }}>Yes (score reduced)</Typography>
              </Box>
            )}

            {(() => {
              const confidenceValue = parseFloat(actualConfidence) || 0;
              const confidencePercent = Math.round(confidenceValue * 100);
              return (
                <Box
                  sx={{
                    display: 'flex',
                    justifyContent: 'space-between',
                    alignItems: 'center',
                    mt: ds.space[1],
                    pt: ds.space[1],
                    borderTop: `1px solid ${ds.gray[200]}`,
                  }}
                >
                  <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>Confidence</Typography>
                  <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.gray[700] }}>{confidencePercent}%</Typography>
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
