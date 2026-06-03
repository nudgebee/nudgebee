import * as React from 'react';
import { styled } from '@mui/material/styles';
import LinearProgress, { linearProgressClasses } from '@mui/material/LinearProgress';
import ValueWithPercentage from '@components1/k8s/common/ValueWithPercentage';
import { tooltipClasses } from '@mui/material/Tooltip';
import { Tooltip, Box, Typography } from '@mui/material';
import PropTypes from 'prop-types';

const BorderLinearProgress = styled(LinearProgress)(({ theme, width = '80%' }) => ({
  width: width,
  height: '6px',
  borderRadius: 'var(--ds-radius-sm)',
  [`&.${linearProgressClasses.colorPrimary},`]: {
    backgroundColor: theme.palette.grey[theme.palette.mode === 'light' ? 200 : 800],
  },
}));

const CustomTooltip = styled(({ className, ...props }) => <Tooltip {...props} classes={{ popper: className }} placement='right' />)(({ theme }) => ({
  [`& .${tooltipClasses.tooltip}`]: {
    backgroundColor: 'var(--ds-background-100)',
    color: 'rgba(0, 0, 0, 0.87)',
    fontSize: theme.typography.pxToRem(12),
    border: '1px solid var(--ds-blue-400)',
    boxShadow: '0px 4px 10px 0px #89899340',
    borderRadius: 'var(--ds-radius-sm)',
    padding: 'var(--ds-space-2)',
  },
}));

const ProgressBar = ({
  value = 0,
  blueVarient = false,
  iconColor = true,
  capacity = '',
  tooltipRequired = false,
  label = '',
  width,
  showCapacity = true,
  showParentheses = false,
}) => {
  let usage = 0;
  let available = 0;
  if (value > 0 && capacity > 0) {
    usage = ((value / capacity) * 100).toFixed(2);
    available = (((capacity - value) / capacity) * 100).toFixed(2);
  }
  const getColor = () => {
    if (usage > 90) {
      return '#F87171';
    }
    return blueVarient ? '#60A5FA' : '#4caf50';
  };

  return (
    <Box sx={{ flexGrow: 1 }} width={width ?? '100%'}>
      {capacity > 0 && value > 0 ? (
        <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          {capacity && showCapacity && <ValueWithPercentage value={label} noPercentage />}
          {!showCapacity && label && <ValueWithPercentage value={label} noPercentage />}
          <ValueWithPercentage value={usage} capacity={capacity} makeValueRed={usage > 90} showParentheses={showParentheses} />
        </Box>
      ) : (
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', alignItems: 'center' }}>
          <ValueWithPercentage value={value} />
        </Box>
      )}
      {tooltipRequired ? (
        <CustomTooltip
          title={
            <Box minWidth={'190px'} display={'flex'} flexDirection={'column'} gap={'10px'}>
              <Box display={'flex'} justifyContent={'space-between'}>
                <Box
                  sx={{
                    fontSize: 'var(--ds-text-caption)',
                    color: 'var(--ds-gray-600)',
                    display: 'flex',
                    alignItems: 'center',
                    fontWeight: 'var(--ds-font-weight-medium)',
                  }}
                >
                  Total
                </Box>
                <Typography display={'flex'} gap={'5px'} fontSize='11px' fontWeight={600}>
                  {capacity}
                  <Typography color={'#9F9F9F'} fontWeight={500} fontSize='11px'>
                    {'(100%)'}
                  </Typography>
                </Typography>
              </Box>
              <Box display={'flex'} justifyContent={'space-between'}>
                <Box
                  sx={{
                    fontSize: 'var(--ds-text-caption)',
                    color: 'var(--ds-gray-600)',
                    display: 'flex',
                    alignItems: 'center',
                    fontWeight: 'var(--ds-font-weight-medium)',
                  }}
                >
                  {!!iconColor && (
                    <Box
                      sx={{
                        width: '8px',
                        height: '8px',
                        borderRadius: 'var(--ds-radius-sm)',
                        backgroundColor: value > 90 ? 'red' : '#3B82F6',
                        mr: 'var(--ds-space-1)',
                      }}
                    />
                  )}
                  Usage
                </Box>
                <Typography display={'flex'} gap={'5px'} fontSize='11px' fontWeight={600}>
                  {value}
                  <Typography color={'#9F9F9F'} fontWeight={500} fontSize='11px'>
                    {'(' + usage + '%)'}
                  </Typography>
                </Typography>
              </Box>
              <Box display={'flex'} justifyContent={'space-between'}>
                <Box
                  sx={{
                    fontSize: 'var(--ds-text-caption)',
                    color: 'var(--ds-gray-600)',
                    display: 'flex',
                    alignItems: 'center',
                    fontWeight: 'var(--ds-font-weight-medium)',
                  }}
                >
                  {!!iconColor && (
                    <Box
                      sx={{
                        width: '8px',
                        height: '8px',
                        borderRadius: 'var(--ds-radius-sm)',
                        backgroundColor: 'var(--ds-gray-200)',
                        mr: 'var(--ds-space-1)',
                      }}
                    />
                  )}
                  Available
                </Box>

                <Typography display={'flex'} gap={'5px'} fontSize='11px' fontWeight={600}>
                  {(capacity - value).toFixed(2)}
                  <Typography color={'#9F9F9F'} fontSize='11px' fontWeight={500}>
                    {`(${available}%)`}
                  </Typography>
                </Typography>
              </Box>
            </Box>
          }
        >
          <BorderLinearProgress
            width={width}
            sx={{
              '& .MuiLinearProgress-bar1Determinate': {
                backgroundColor: getColor(),
              },
            }}
            variant='determinate'
            value={usage > 100 ? 100 : usage}
          />
        </CustomTooltip>
      ) : (
        <BorderLinearProgress
          sx={{
            '& .MuiLinearProgress-bar1Determinate': {
              backgroundColor: getColor(),
            },
          }}
          variant='determinate'
          value={value > 100 ? 100 : value}
        />
      )}
    </Box>
  );
};

export default ProgressBar;

ProgressBar.propTypes = {
  value: PropTypes.any,
  blueVarient: PropTypes.bool,
  iconColor: PropTypes.bool,
  capacity: PropTypes.any,
  tooltipRequired: PropTypes.bool,
  width: PropTypes.string,
  label: PropTypes.string,
  showCapacity: PropTypes.bool,
  showParentheses: PropTypes.bool,
};
