import { useMemo, useState } from 'react';
import { styled } from '@mui/material/styles';
import LinearProgress, { linearProgressClasses } from '@mui/material/LinearProgress';
import { Box, Typography, Popover } from '@mui/material';
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined';
import PersonOutlineOutlinedIcon from '@mui/icons-material/PersonOutlineOutlined';
import PropTypes from 'prop-types';
import { ds } from 'src/utils/colors';

const ScoreProgress = styled(LinearProgress)(() => ({
  width: ds.space.mul(0, 25),
  height: ds.space.mul(0, 3),
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

// "control_plane" -> "Control Plane", "escalating" -> "Escalating".
const titleCase = (s) =>
  s
    ? String(s)
        .replace(/_/g, ' ')
        .replace(/\b\w/g, (c) => c.toUpperCase())
    : '';

const envLabel = (env) => {
  if (env === 'prod') {
    return 'Production';
  }
  if (env === 'non_prod' || env === 'non-prod') {
    return 'Non-Production';
  }
  return 'Default';
};

// Shared styles for the breakdown label/value rows.
const rowSx = { display: 'flex', justifyContent: 'space-between', alignItems: 'center' };
const labelSx = { fontSize: ds.text.small, color: ds.gray[600] };
const valueSx = { fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.gray[700] };

const ScoreDisplay = ({ score, priority: _priority, scoreFactors, confidence, children }) => {
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

  const isHuman = factors.scoring_path === 'human_override' || (typeof factors.authority === 'string' && factors.authority.startsWith('human:'));
  const isLLM = !isHuman && factors.scoring_path === 'llm_verdict';
  const correctionScope = factors.authority === 'human:class' ? 'this alert class' : 'this alert';
  const actualConfidence =
    factors.verdict_confidence !== undefined ? factors.verdict_confidence : factors.confidence !== undefined ? factors.confidence : confidence;

  if (score === null || score === undefined) {
    const dash = <Typography sx={{ color: ds.gray[400], fontSize: ds.text.small, textAlign: 'center' }}>-</Typography>;
    if (!children) {
      return dash;
    }
    return (
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1] }}>
        {dash}
        {children}
      </Box>
    );
  }

  const scoreColor = getScoreColor(score);

  return (
    <>
      <Box
        data-testid='score-display'
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: ds.space[1],
          cursor: 'default',
        }}
      >
        {/* Hover regions are scoped to score/bar and the icon (not the whole row) so the
            children slot — e.g. the priority pin dropdown — doesn't trigger the popover. */}
        <Box onMouseEnter={handleMouseEnter} onMouseLeave={handleMouseLeave} sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1] }}>
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
        </Box>
        {children}
        <Box onMouseEnter={handleMouseEnter} onMouseLeave={handleMouseLeave} sx={{ display: 'flex', alignItems: 'center' }}>
          {isHuman ? (
            <PersonOutlineOutlinedIcon
              data-testid='score-corrected-icon'
              titleAccess='Manually corrected'
              sx={{ fontSize: ds.text.small, color: ds.blue?.[500] || ds.gray[600] }}
            />
          ) : (
            <InfoOutlinedIcon sx={{ fontSize: ds.text.small, color: ds.gray[400] }} />
          )}
        </Box>
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
            minWidth: ds.space.mul(0, 140),
            maxWidth: ds.space.mul(0, 180),
          },
        }}
        disableRestoreFocus
      >
        <Box sx={{ p: ds.space[4], minWidth: ds.space.mul(0, 120) }}>
          <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: ds.space[4] }}>
            <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>Priority Score</Typography>
          </Box>

          {isHuman ? (
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                gap: ds.space[1],
                mb: ds.space[2],
                px: ds.space[2],
                py: ds.space[1],
                borderRadius: ds.radius.sm,
                backgroundColor: ds.blue?.[100] || ds.gray[100],
              }}
            >
              <PersonOutlineOutlinedIcon sx={{ fontSize: ds.text.small, color: ds.blue?.[500] || ds.gray[600] }} />
              <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.blue?.[600] || ds.gray[700] }}>
                Corrected by a human — overrides the model for {correctionScope}
              </Typography>
            </Box>
          ) : null}

          <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
            {isHuman ? (
              <>
                <Box sx={rowSx}>
                  <Typography sx={labelSx}>Set priority</Typography>
                  <Typography sx={valueSx}>{factors.corrected_priority || _priority}</Typography>
                </Box>

                {factors.category || factors.intrinsic || factors.reasoning ? (
                  <Box
                    sx={{
                      mt: ds.space[1],
                      pt: ds.space[1],
                      borderTop: `1px solid ${ds.gray[200]}`,
                      display: 'flex',
                      flexDirection: 'column',
                      gap: ds.space[1],
                    }}
                  >
                    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], fontStyle: 'italic' }}>Model had suggested</Typography>
                    {factors.category ? (
                      <Box sx={rowSx}>
                        <Typography sx={labelSx}>Category</Typography>
                        <Typography sx={valueSx}>{factors.category}</Typography>
                      </Box>
                    ) : null}
                    {factors.intrinsic ? (
                      <Box sx={rowSx}>
                        <Typography sx={labelSx}>Severity</Typography>
                        <Typography sx={valueSx}>{titleCase(factors.intrinsic)}</Typography>
                      </Box>
                    ) : null}
                    {factors.reasoning ? (
                      <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600], lineHeight: 1.4 }}>{factors.reasoning}</Typography>
                    ) : null}
                  </Box>
                ) : null}
              </>
            ) : isLLM ? (
              <>
                {factors.reasoning ? (
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1], mb: ds.space[1] }}>
                    <Typography sx={labelSx}>Why</Typography>
                    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], lineHeight: 1.4 }}>{factors.reasoning}</Typography>
                  </Box>
                ) : null}

                {factors.category ? (
                  <Box sx={rowSx}>
                    <Typography sx={labelSx}>Category</Typography>
                    <Typography sx={valueSx}>{factors.category}</Typography>
                  </Box>
                ) : null}

                <Box sx={rowSx}>
                  <Typography sx={labelSx}>Severity</Typography>
                  <Typography sx={valueSx}>{titleCase(factors.intrinsic)}</Typography>
                </Box>

                {factors.blast ? (
                  <Box sx={rowSx}>
                    <Typography sx={labelSx}>Blast radius</Typography>
                    <Typography sx={valueSx}>{titleCase(factors.blast)}</Typography>
                  </Box>
                ) : null}

                <Box sx={rowSx}>
                  <Typography sx={labelSx}>Environment</Typography>
                  <Typography sx={valueSx}>{envLabel(factors.env_category)}</Typography>
                </Box>

                {factors.recurrence_semantics && factors.recurrence_semantics !== 'neutral' ? (
                  <Box sx={rowSx}>
                    <Typography sx={labelSx}>Recurrence</Typography>
                    <Typography sx={valueSx}>{titleCase(factors.recurrence_semantics)}</Typography>
                  </Box>
                ) : null}

                {factors.band ? (
                  <Box sx={rowSx}>
                    <Typography sx={labelSx}>Priority band</Typography>
                    <Typography sx={valueSx}>{factors.band}</Typography>
                  </Box>
                ) : null}

                {factors.correlation_type ? (
                  <Box sx={rowSx}>
                    <Typography sx={labelSx}>Correlation</Typography>
                    <Typography sx={valueSx}>{titleCase(factors.correlation_type)}</Typography>
                  </Box>
                ) : null}
              </>
            ) : (
              <>
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
              </>
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
  children: PropTypes.node,
};

export default ScoreDisplay;
