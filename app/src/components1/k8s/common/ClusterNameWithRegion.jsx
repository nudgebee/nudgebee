import { Box, Typography } from '@mui/material';
import eksIcon from '@assets/amazon-eks-icon.svg';
import Tooltip from '@components1/ds/Tooltip';
import SafeIcon from '@components1/common/SafeIcon';
import PropTypes from 'prop-types';
import Text from '@common-new/format/Text';

const ClusterNameWithRegion = ({
  name = '',
  font = '13px',
  fontWeight = 400,
  region = <></>,
  nameOnClick = (_e) => {},
  namespace = '',
  hideIcon = false,
  cursorPointer = false,
  maxWidth = '200px',
  additionalContent = <></>,
  isTargetURL = false,
  namespaceFont = '10px',
  namespaceSx = {},
  showTruncatedString = false,
  smallScreenWidth = '',
  nameMaxLength = 50,
  tooltipClassName = '',
  showAutoEllipsis = false,
  lineClamp = 1,
  wordBreak = false,
  showTooltip = false,
  ...rest
}) => {
  const truncatedString = name.substring(0, nameMaxLength) + (name.length > nameMaxLength ? '...' : '');
  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        gap: namespace ? 0 : 'var(--ds-space-1)',
        maxWidth: maxWidth,
        cursor: cursorPointer ? 'pointer' : 'unset',
        '@media(max-width: 1100px)': {
          maxWidth: `${smallScreenWidth} !important`,
          p: {
            fontSize: 'var(--ds-text-body) !important',
          },
        },
      }}
      {...rest}
    >
      {showAutoEllipsis ? (
        <Text
          value={name}
          showAutoEllipsis
          lineClamp={lineClamp}
          sx={{
            fontSize: font || 'var(--ds-text-body-lg)',
            fontWeight: fontWeight || 400,
            color: isTargetURL ? 'var(--ds-blue-600)' : 'var(--ds-gray-700)',
            width: '100%',
            '&:hover': {
              textDecoration: isTargetURL ? 'underline' : 'none',
            },
          }}
          onClick={nameOnClick}
        />
      ) : (
        <Tooltip title={showTooltip ? name : ''} tooltipClassName={tooltipClassName}>
          <Typography
            {...(!showTruncatedString && !wordBreak && { noWrap: true })}
            sx={{
              fontSize: font || 'var(--ds-text-body-lg)',
              fontWeight: fontWeight || 400,
              width: '100%',
              color: isTargetURL ? 'var(--ds-blue-600)' : 'var(--ds-gray-700)',
              ...(wordBreak && {
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-word',
              }),
              '&:hover': {
                textDecoration: isTargetURL ? 'underline' : 'none',
              },
            }}
            onClick={nameOnClick}
          >
            {showTruncatedString ? truncatedString : name}
          </Typography>
        </Tooltip>
      )}

      {region && (
        <Typography
          sx={{
            fontSize: 'var(--ds-text-small)',
            color: 'var(--ds-gray-600)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'flex-start',
            gap: 'var(--ds-space-2)',
          }}
        >
          {!hideIcon && (
            <>
              <SafeIcon src={eksIcon} alt='' />
              <span
                style={{
                  width: '1px',
                  height: '13px',
                  backgroundColor: 'var(--ds-gray-600)',
                }}
              />
            </>
          )}
          {region}
        </Typography>
      )}
      {additionalContent ? <div>{additionalContent}</div> : null}
      {namespace && (
        <Typography sx={{ namespaceSx, fontSize: namespaceFont || 'var(--ds-text-caption)', color: 'var(--ds-gray-400)', mt: '-2px' }}>
          {namespace}
        </Typography>
      )}
    </Box>
  );
};

export default ClusterNameWithRegion;

ClusterNameWithRegion.propTypes = {
  font: PropTypes.string,
  fontWeight: PropTypes.number,
  name: PropTypes.string,
  region: PropTypes.string,
  nameOnClick: PropTypes.func,
  namespace: PropTypes.string,
  hideIcon: PropTypes.bool,
  cursorPointer: PropTypes.bool,
  maxWidth: PropTypes.string,
  additionalContent: PropTypes.node,
  isTargetURL: PropTypes.bool,
  showTruncatedString: PropTypes.bool,
  namespaceFont: PropTypes.string,
  smallScreenWidth: PropTypes.any,
  nameMaxLength: PropTypes.number,
  namespaceSx: PropTypes.object,
  tooltipClassName: PropTypes.string,
  lineClamp: PropTypes.number,
  wordBreak: PropTypes.bool,
  showTooltip: PropTypes.bool,
};
