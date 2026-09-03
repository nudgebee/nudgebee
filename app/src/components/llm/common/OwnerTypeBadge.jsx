import React from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import { ds } from '@utils/colors';
import { useBrandingConfig } from '@hooks/useTenantBranding';

/**
 * "<Brand> System Agent" / "User Created Agent" pill for the agent and tool listings.
 *
 * Resolves the brand title itself rather than taking it as a prop. Both callers
 * build their table rows once inside an effect and keep the resulting elements in
 * state, so a title interpolated by the caller freezes at whatever branding had
 * loaded when the rows were built — which is how white-label tenants ended up with
 * a permanent "NUDGEBEE SYSTEM AGENT" badge. Reading branding here keeps the text
 * subscribed, so it repaints when the config lands however late that is.
 */
const OwnerTypeBadge = ({ type, systemSuffix, userLabel }) => {
  const { title: baseTitle } = useBrandingConfig();
  const isSystem = type === 'system';

  return (
    <Box
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        backgroundColor: isSystem ? ds.blue[100] : ds.gray[100],
        color: isSystem ? ds.blue[700] : ds.gray[600],
        fontSize: ds.text.caption,
        fontWeight: ds.weight.semibold,
        padding: '2px 6px',
        borderRadius: ds.radius.pill,
        border: `1px solid ${isSystem ? ds.blue[200] : ds.gray[200]}`,
        textTransform: 'uppercase',
        letterSpacing: '0.5px',
        width: 'fit-content',
      }}
    >
      {isSystem ? `${baseTitle} ${systemSuffix}` : userLabel}
    </Box>
  );
};

OwnerTypeBadge.propTypes = {
  type: PropTypes.string,
  systemSuffix: PropTypes.string.isRequired,
  userLabel: PropTypes.string.isRequired,
};

export default OwnerTypeBadge;
