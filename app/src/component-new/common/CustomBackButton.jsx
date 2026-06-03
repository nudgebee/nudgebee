/**
 * CustomBackButton — DS V2 internals (router-back domain adapter).
 *
 * Thin wrapper around `@components1/ds/Button` that encapsulates the
 * "go back, fall back to /" router logic so callsites don't have to repeat it.
 *
 * Public API: { id, onClick, backButtonPath }.
 */
'use client';

import { useCallback } from 'react';
import { useRouter } from 'next/router';
import PropTypes from 'prop-types';
import ArrowBackIcon from '@mui/icons-material/ArrowBack';
import { Button as DsButton } from '@components1/ds/Button';

const CustomBackButton = ({ id, onClick, backButtonPath }) => {
  const router = useRouter();

  const handleBack = useCallback(() => {
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
  }, [onClick, router, backButtonPath]);

  return (
    <DsButton
      id={id}
      composition='icon-only'
      tone='secondary'
      size='md'
      tooltip='Go Back'
      tooltipPlacement='bottom'
      aria-label='Go back'
      onClick={handleBack}
      icon={<ArrowBackIcon style={{ width: 20, height: 20 }} />}
    />
  );
};

CustomBackButton.propTypes = {
  id: PropTypes.string,
  onClick: PropTypes.func,
  backButtonPath: PropTypes.string,
};

export default CustomBackButton;
