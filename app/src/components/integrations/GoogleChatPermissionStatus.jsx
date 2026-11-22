import React, { useState, useEffect, useCallback } from 'react';
import { Stack, CircularProgress } from '@mui/material';
import { Button as DsButton } from '@ui/Button';
import { Label } from '@ui/Label';
import apiIntegrations from '@api1/integrations';
import { canManage } from '@lib/auth';

// Mirrors GoogleChatSpacesPanel.jsx's own header actions exactly (same
// tones, copy, admin-console URL and permission check) — duplicated rather
// than imported, because that component has no test coverage; same
// reasoning as DiscordInstallModal.jsx / chatOAuthInstall.ts (#41112).
const GOOGLE_ADMIN_APPS_URL = 'https://admin.google.com/ac/apps/unified';
const PERMISSION_TONE = {
  authorized: 'success',
  needs_authorization: 'warning',
  no_spaces: 'neutral',
  sa_not_configured: 'critical',
};
const PERMISSION_TEXT = {
  authorized: 'Join permission: authorized',
  needs_authorization: 'Join permission: needs admin authorization',
  no_spaces: 'Join permission: connect a space to check',
  sa_not_configured: 'Google Chat is not configured on this deployment',
  unknown: 'Join permission: unknown',
};

/**
 * The two Google Chat actions a tenant admin can actually complete without
 * leaving Setup Center: check, or grant, the bot's self-join permission.
 * Binding a specific space can't come along for the ride — that only
 * happens on /accounts/account-form, which reads the bot's own Connect-card
 * deep-link (see SetupCenter.tsx's Google Chat instructions for why).
 */
const GoogleChatPermissionStatus = () => {
  const [status, setStatus] = useState('unknown');
  const [checking, setChecking] = useState(false);
  const userCanManage = canManage('messagingplatforms', 'Write');

  const checkPermission = useCallback(async () => {
    setChecking(true);
    try {
      const res = await apiIntegrations.getGoogleChatPermissionStatus();
      setStatus(res?.status || 'unknown');
    } catch {
      setStatus('unknown');
    } finally {
      setChecking(false);
    }
  }, []);

  useEffect(() => {
    checkPermission();
  }, [checkPermission]);

  return (
    <Stack direction='row' spacing={1} alignItems='center' sx={{ flexWrap: 'wrap' }}>
      {checking ? <CircularProgress size={16} /> : <Label tone={PERMISSION_TONE[status] || 'neutral'} text={PERMISSION_TEXT[status]} size='sm' />}
      {userCanManage ? (
        <>
          <DsButton id='gchat-authorize-btn' tone='secondary' size='sm' onClick={() => window.open(GOOGLE_ADMIN_APPS_URL, '_blank', 'noopener')}>
            Authorize in Google Workspace
          </DsButton>
          <DsButton id='gchat-recheck-permission-btn' tone='secondary' size='sm' loading={checking} onClick={checkPermission}>
            Re-check
          </DsButton>
        </>
      ) : null}
    </Stack>
  );
};

export default GoogleChatPermissionStatus;
