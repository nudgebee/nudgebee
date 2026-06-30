import { useEffect, useMemo, useState, useCallback } from 'react';
import { useRouter } from 'next/router';
import { Box, Stack, Typography, CircularProgress } from '@mui/material';
import DeleteOutlineIcon from '@mui/icons-material/DeleteOutline';
import { Button as DsButton } from '@ui/Button';
import { Label } from '@ui/Label';
import { Modal } from '@ui/Modal';
import apiIntegrations from '@api1/integrations';
import { isTenantAdmin } from '@lib/auth';

const GOOGLE_CHAT_SPACE_TYPE = 'google_chat_space';
const SPACE_ID_PATTERN = /^spaces\/[A-Za-z0-9_-]+$/;

function extractDisplayName(configValues) {
  try {
    const arr = typeof configValues === 'string' ? JSON.parse(configValues) : configValues;
    if (Array.isArray(arr)) {
      return arr.find((c) => c?.name === 'display_name')?.value || '';
    }
  } catch {
    // fall through to empty
  }
  return '';
}

function formatBoundAt(value) {
  if (!value) return '—';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString();
}

/**
 * Google Chat integration panel (service-account model).
 *
 * Manages the spaces bound to this tenant (google_chat_space integrations):
 * lists who/when each was bound, supports unbinding, and — when reached via the
 * bot's "Connect" card deep-link (?space_id=…&display_name=…) — shows the
 * incoming space as a pending row with a Connect action.
 */
export default function GoogleChatSpacesPanel() {
  const router = useRouter();
  const incomingSpaceId = typeof router.query.space_id === 'string' ? router.query.space_id : '';
  const incomingDisplayName = typeof router.query.display_name === 'string' ? router.query.display_name : '';

  const userIsTenantAdmin = isTenantAdmin();

  const [spaces, setSpaces] = useState([]);
  const [loading, setLoading] = useState(true);
  const [connecting, setConnecting] = useState(false);
  const [deleting, setDeleting] = useState('');
  const [toDelete, setToDelete] = useState(null);
  const [notice, setNotice] = useState(null);

  const loadSpaces = useCallback(async () => {
    try {
      const res = await apiIntegrations.listIntegrations({ type: GOOGLE_CHAT_SPACE_TYPE, limit: 200 });
      const rows = res?.data?.data?.integrations_list?.rows || [];
      setSpaces(
        rows.map((row) => ({
          id: row.id,
          spaceId: row.name,
          displayName: extractDisplayName(row.integration_config_values) || row.name,
          boundBy: row.created_by_display_name || '—',
          boundAt: row.created_at,
          status: row.status,
        }))
      );
    } catch (error) {
      console.error('Failed to load Google Chat spaces:', error);
      setSpaces([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadSpaces();
  }, [loadSpaces]);

  const incomingValid = useMemo(() => SPACE_ID_PATTERN.test(incomingSpaceId), [incomingSpaceId]);
  const alreadyBound = useMemo(() => spaces.some((s) => s.spaceId === incomingSpaceId), [spaces, incomingSpaceId]);
  const showPendingRow = incomingValid && !alreadyBound && !loading;

  const handleConnect = async () => {
    setConnecting(true);
    setNotice(null);
    try {
      const response = await apiIntegrations.addIntegrations({
        integration_name: GOOGLE_CHAT_SPACE_TYPE,
        integration_config_name: incomingSpaceId,
        integration_config_values: [
          { name: 'display_name', value: incomingDisplayName },
          { name: 'space_type', value: 'SPACE' },
        ],
        account_ids: [],
        source: 'user',
        skip_validation: true,
      });
      const gqlError = response?.errors?.[0]?.message || response?.data?.errors?.[0]?.message || response?.message || '';
      if (gqlError) {
        if (gqlError.toLowerCase().includes('already exists')) {
          setNotice({ tone: 'warning', text: 'This space is already bound to a Nudgebee organization.' });
        } else {
          setNotice({ tone: 'critical', text: gqlError });
        }
      } else if (response?.data?.data?.integrations_create_config?.id) {
        setNotice({ tone: 'success', text: 'Space connected. Return to Google Chat and retry your message.' });
      }
      await loadSpaces();
    } catch (err) {
      setNotice({ tone: 'critical', text: err?.message || 'Failed to connect this space.' });
    } finally {
      setConnecting(false);
    }
  };

  const handleDelete = async () => {
    if (!toDelete) return;
    setDeleting(toDelete.spaceId);
    try {
      const response = await apiIntegrations.deleteIntegrations({
        integration_name: GOOGLE_CHAT_SPACE_TYPE,
        integration_config_name: toDelete.spaceId,
        source: 'user',
      });
      const gqlError = response?.errors?.[0]?.message || response?.data?.errors?.[0]?.message || response?.message || '';
      if (gqlError) {
        setNotice({ tone: 'critical', text: gqlError });
      } else {
        setNotice({ tone: 'success', text: `Unbound “${toDelete.displayName}”.` });
      }
      await loadSpaces();
    } catch (err) {
      setNotice({ tone: 'critical', text: err?.message || 'Failed to unbind this space.' });
    } finally {
      setDeleting('');
      setToDelete(null);
    }
  };

  const headerCell = { fontWeight: 600, color: 'text.secondary', fontSize: 'var(--ds-text-small)' };
  const gridCols = userIsTenantAdmin ? '2fr 1.4fr 1.4fr 0.8fr 48px' : '2fr 1.4fr 1.4fr 0.8fr';

  return (
    <Box sx={{ maxWidth: 980 }} data-testid='gchat-spaces-panel'>
      <Typography variant='h6' sx={{ mb: 1 }}>
        Google Chat
      </Typography>
      <Typography variant='body2' sx={{ color: 'text.secondary', mb: 3 }}>
        Google Chat connects through the Nudgebee bot — no user sign-in required. Add the bot to a space; when it posts the <strong>Connect</strong>{' '}
        card, a tenant admin binds that space here. Bound spaces become notification-rule destinations.
      </Typography>

      {notice ? (
        <Box sx={{ mb: 2 }}>
          <Label tone={notice.tone} text={notice.text} size='md' />
        </Box>
      ) : null}

      {showPendingRow ? (
        <Box sx={{ p: 2, mb: 3, border: '1px solid', borderColor: 'warning.main', borderRadius: 1 }} data-testid='gchat-pending-row'>
          <Stack direction='row' alignItems='center' justifyContent='space-between' spacing={2}>
            <Box>
              <Stack direction='row' spacing={1} alignItems='center'>
                <Typography variant='subtitle2'>{incomingDisplayName || incomingSpaceId}</Typography>
                <Label tone='warning' text='Pending' size='sm' />
              </Stack>
              <Typography variant='caption' sx={{ color: 'text.secondary' }}>
                {incomingSpaceId}
              </Typography>
            </Box>
            {userIsTenantAdmin ? (
              <DsButton data-testid='gchat-connect-submit' disabled={connecting} onClick={handleConnect}>
                {connecting ? <CircularProgress size={16} /> : 'Connect'}
              </DsButton>
            ) : (
              <Typography variant='caption' sx={{ color: 'text.secondary' }}>
                Ask a tenant admin to connect
              </Typography>
            )}
          </Stack>
        </Box>
      ) : null}

      <Typography variant='subtitle2' sx={{ mb: 1 }}>
        Connected spaces
      </Typography>

      {loading ? (
        <CircularProgress size={20} />
      ) : spaces.length === 0 ? (
        <Typography variant='body2' sx={{ color: 'text.secondary' }} data-testid='gchat-no-spaces'>
          No spaces connected yet. Add the bot to a Google Chat space to get started.
        </Typography>
      ) : (
        <Box data-testid='gchat-spaces-list' sx={{ border: '1px solid', borderColor: 'divider', borderRadius: 1, overflow: 'hidden' }}>
          <Box sx={{ display: 'grid', gridTemplateColumns: gridCols, gap: 2, px: 2, py: 1.25, bgcolor: 'action.hover' }}>
            <Typography sx={headerCell}>Space</Typography>
            <Typography sx={headerCell}>Bound by</Typography>
            <Typography sx={headerCell}>Bound at</Typography>
            <Typography sx={headerCell}>Status</Typography>
            {userIsTenantAdmin ? <span /> : null}
          </Box>
          {spaces.map((space) => (
            <Box
              key={space.id}
              sx={{
                display: 'grid',
                gridTemplateColumns: gridCols,
                gap: 2,
                alignItems: 'center',
                px: 2,
                py: 1.5,
                borderTop: '1px solid',
                borderColor: 'divider',
              }}
            >
              <Box sx={{ minWidth: 0 }}>
                <Typography variant='body2' noWrap title={space.displayName}>
                  {space.displayName}
                </Typography>
                <Typography variant='caption' sx={{ color: 'text.secondary' }} noWrap title={space.spaceId}>
                  {space.spaceId}
                </Typography>
              </Box>
              <Typography variant='body2' noWrap title={space.boundBy}>
                {space.boundBy}
              </Typography>
              <Typography variant='body2' sx={{ color: 'text.secondary' }}>
                {formatBoundAt(space.boundAt)}
              </Typography>
              <Box>
                <Label
                  tone={space.status === 'disabled' ? 'neutral' : 'success'}
                  text={space.status === 'disabled' ? 'Disabled' : 'Active'}
                  size='sm'
                />
              </Box>
              {userIsTenantAdmin ? (
                <DsButton
                  data-testid={`gchat-delete-${space.spaceId}`}
                  tone='ghost'
                  size='sm'
                  composition='icon-only'
                  aria-label={`Unbind ${space.displayName}`}
                  tooltip='Unbind space'
                  disabled={deleting === space.spaceId}
                  icon={<DeleteOutlineIcon fontSize='small' />}
                  onClick={() => setToDelete(space)}
                />
              ) : null}
            </Box>
          ))}
        </Box>
      )}

      <Modal
        open={!!toDelete}
        onClose={() => setToDelete(null)}
        title='Unbind Google Chat space?'
        subtitle={toDelete ? `${toDelete.displayName} (${toDelete.spaceId})` : ''}
        confirmText='Unbind'
        cancelText='Cancel'
        loading={!!deleting}
        onConfirm={handleDelete}
      >
        <Typography variant='body2'>
          Notifications routed to this space will stop, and the bot will be disconnected from it. You can reconnect later from the space&apos;s
          Connect card.
        </Typography>
      </Modal>
    </Box>
  );
}
