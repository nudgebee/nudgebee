/**
 * @deprecated Use <Button tone="link" icon={<ArrowBack/>}>Back</Button> from '@components1/ds/Button' instead.
 * Tracked for removal 2026-06-06 (30-day deprecation clock from V2 ship 2026-05-07).
 */
'use client';

import { useCallback } from 'react';
import { IconButton, Tooltip, tooltipClasses } from '@mui/material';
import { useRouter } from 'next/router';
import PropTypes from 'prop-types';
import ArrowLeftIcon from '@assets/arrow-left.svg';
import { colors } from 'src/utils/colors';
import { ArrowBackGrayIcon } from '@assets';
import SafeIcon from './SafeIcon';

const CustomBackArrow = ({ id, onClick, useNewIcon = false, backButtonPath }) => {
  const router = useRouter();

  const handleBack = useCallback(
    (_e) => {
      if (onClick) {
        onClick();
        return;
      }
      if (backButtonPath) {
        router.push(backButtonPath);
        return;
      }
      if (window.history.length > 1) {
        router.back();
      } else {
        router.push('/');
      }
    },
    [onClick, router, backButtonPath]
  );

  return (
    <>
      {useNewIcon ? (
        <Tooltip
          title='Go Back'
          placement='bottom'
          slotProps={{
            popper: {
              sx: {
                [`&.${tooltipClasses.popper}[data-popper-placement*="right"] .${tooltipClasses.tooltip}`]: {
                  marginLeft: '2px',
                },
              },
            },
          }}
        >
          <IconButton
            id={id}
            size='small'
            sx={{
              border: `1px solid ${colors.border.secondary}`,
              borderRadius: '4px',
              height: '28px',
              width: '28px',
              '&:hover': {
                border: `0.5px solid ${colors.border.success}`,
                '& img,svg,path': {
                  filter: 'brightness(0) saturate(100%) invert(54%) sepia(92%) saturate(402%) hue-rotate(89deg) brightness(95%) contrast(90%)',
                },
              },
            }}
            onClick={handleBack}
          >
            <SafeIcon src={ArrowBackGrayIcon} alt='arrow back' priority style={{ cursor: 'pointer' }} />
          </IconButton>
        </Tooltip>
      ) : (
        <SafeIcon
          src={ArrowLeftIcon}
          alt='arrow back'
          priority
          style={{ marginTop: '12px', cursor: 'pointer' }}
          className='go-back'
          onClick={handleBack}
        />
      )}
    </>
  );
};

CustomBackArrow.propTypes = {
  id: PropTypes.string,
  onClick: PropTypes.func,
  useNewIcon: PropTypes.bool,
  backButtonPath: PropTypes.string,
};

export default CustomBackArrow;
