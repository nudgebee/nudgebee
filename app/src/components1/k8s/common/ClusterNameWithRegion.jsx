import { Box, Typography } from '@mui/material';
import React from 'react';
import eksIcon from '@assets/amazon-eks-icon.svg';
import CustomTooltip from '@common/CustomTooltip';
import SafeIcon from '@components1/common/SafeIcon';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import { Text } from '@components1/common';

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
        gap: namespace ? 0 : '3px',
        maxWidth: maxWidth,
        cursor: cursorPointer ? 'pointer' : 'unset',
        '@media(max-width: 1100px)': {
          maxWidth: `${smallScreenWidth} !important`,
          p: {
            fontSize: '13px !important',
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
            fontSize: font || '14px',
            fontWeight: fontWeight || 400,
            color: isTargetURL ? colors.text.primary : colors.text.secondary,
            width: '100%',
            '&:hover': {
              textDecoration: isTargetURL ? 'underline' : 'none',
            },
          }}
          onClick={nameOnClick}
        />
      ) : (
        <CustomTooltip title={showTooltip ? name : ''} tooltipClassName={tooltipClassName}>
          <Typography
            {...(!showTruncatedString && !wordBreak && { noWrap: true })}
            sx={{
              fontSize: font || '14px',
              fontWeight: fontWeight || 400,
              width: '100%',
              color: isTargetURL ? colors.text.primary : colors.text.secondary,
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
        </CustomTooltip>
      )}

      {region && (
        <Typography
          sx={{
            fontSize: '12px',
            color: colors.text.tertiary,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'flex-start',
            gap: '6px',
          }}
        >
          {!hideIcon && (
            <>
              <SafeIcon src={eksIcon} alt='' />
              <span
                style={{
                  width: '1px',
                  height: '13px',
                  backgroundColor: colors.background.tertiary,
                }}
              />
            </>
          )}
          {region}
        </Typography>
      )}
      {additionalContent ? <div>{additionalContent}</div> : null}
      {namespace && (
        <Typography sx={{ namespaceSx, fontSize: namespaceFont || '10px', color: colors.background.secondaryDark, mt: '-2px' }}>
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
