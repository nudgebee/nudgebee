import React, { useEffect, useState } from 'react';
import { useRouter } from 'next/router';
import { Box, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { Banner } from '@ui/Banner';
import { Button } from '@ui/Button';
import { ToggleGroup } from '@ui/ToggleGroup';
import FilterDropdownButton from '@ui/FilterDropdown';
import { toast as snackbar } from '@ui/Toast';
import { ds } from 'src/utils/colors';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import InfoIcon from '@mui/icons-material/Info';
import apiUser from '@api1/user/';
import { getAppBaseUrl } from '@lib/externalUrls';
import { canManageGatewayUserAccess, canViewGatewayUserAccess } from '@lib/auth';
import { listTenantUsers } from '@api1/gateway-config';
import TokenList from './TokenList';
import TenantTokenList from './TenantTokenList';
import CreateTokenDialog from './CreateTokenDialog';
import { EditGatewayAccessDialog } from './GatewayRestrictions';
import { KIND_LABELS } from './keyKinds';

const TENANT_PAGE_SIZE = 50;
const NO_TENANT_FILTER = { userId: '', kind: '' };
const KIND_FILTER_OPTIONS = Object.entries(KIND_LABELS).map(([value, label]) => ({ value, label }));
// Where an admin narrows a user without revoking their keys.
const USER_ACCESS_PATH = '/user-management#ai-tools/gateway/access';

// `defaultKind` is the type the Create form starts on. Pass a single-purpose kind, never
// 'all': a key that opens both the app API and the gateway must only ever be picked on purpose.
const ApiTokens = ({ open, title, onClose, defaultKind = 'platform' }) => {
  const { title: baseTitle } = useBrandingConfig();
  const [loading, setLoading] = useState(false);
  const [tokens, setTokens] = useState([]);
  const [showCreateDialog, setShowCreateDialog] = useState(false);
  const [createdToken, setCreatedToken] = useState(null);
  const [createdTokenName, setCreatedTokenName] = useState(null);
  const [showInstructions, setShowInstructions] = useState(false);
  const [deleteDialog, setDeleteDialog] = useState({ open: false, token: null });
  const [editAccessToken, setEditAccessToken] = useState(null);
  const [savingAccess, setSavingAccess] = useState(false);
  const isKeyAdmin = canViewGatewayUserAccess();
  const canRevokeAny = canManageGatewayUserAccess();
  const router = useRouter();
  const [view, setView] = useState('mine');
  const [tenantTokens, setTenantTokens] = useState({ tokens: [], total: 0 });
  const [tenantPage, setTenantPage] = useState(0);
  const [tenantPageSize, setTenantPageSize] = useState(TENANT_PAGE_SIZE);
  const [tenantError, setTenantError] = useState(null);
  const [tenantLoaded, setTenantLoaded] = useState(false);
  const [tenantFilter, setTenantFilter] = useState(NO_TENANT_FILTER);
  const [ownerOptions, setOwnerOptions] = useState([]);
  const [adminRevoke, setAdminRevoke] = useState(null);

  useEffect(() => {
    if (open) {
      // The modal stays mounted between opens, so each open starts back on My keys.
      setView('mine');
      setTenantTokens({ tokens: [], total: 0 });
      setTenantPage(0);
      setTenantPageSize(TENANT_PAGE_SIZE);
      setTenantError(null);
      setTenantLoaded(false);
      setTenantFilter(NO_TENANT_FILTER);
      setAdminRevoke(null);
      fetchTokens();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const fetchTokens = async () => {
    try {
      setLoading(true);
      const response = await apiUser.listUserTokens();
      if (response.errors && response.errors.length > 0) {
        snackbar.error(`Failed to fetch API tokens - ${parseHttpResponseBodyMessage(response)}`);
        setTokens([]);
      } else {
        setTokens(response.data || []);
      }
    } catch (error) {
      snackbar.error(`Failed to fetch API tokens - ${parseHttpResponseBodyMessage(error)}`);
      setTokens([]);
    } finally {
      setLoading(false);
    }
  };

  const fetchTenantTokens = async (page = tenantPage, filter = tenantFilter, size = tenantPageSize) => {
    try {
      setLoading(true);
      const response = await apiUser.listTenantTokens({
        ...(filter.userId && { userId: filter.userId }),
        ...(filter.kind && { kind: filter.kind }),
        limit: size,
        offset: page * size,
      });
      if (response.errors && response.errors.length > 0) {
        const message = `Failed to fetch tenant API keys - ${parseHttpResponseBodyMessage(response)}`;
        setTenantError(message);
        snackbar.error(message);
        return;
      }
      setTenantError(null);
      const { tokens: rows = [], total = 0 } = response.data;
      // A revoke can empty the last page; step back once, only to an earlier page.
      const lastPage = Math.max(0, Math.ceil(total / size) - 1);
      if (rows.length === 0 && lastPage < page) {
        await fetchTenantTokens(lastPage, filter, size);
        return;
      }
      setTenantTokens(response.data);
      setTenantPage(page);
      setTenantPageSize(size);
      setTenantLoaded(true);
    } finally {
      setLoading(false);
    }
  };

  const changeTenantFilter = (patch) => {
    const next = { ...tenantFilter, ...patch };
    setTenantFilter(next);
    fetchTenantTokens(0, next);
  };

  // Owner names for the filter; without them the filter still offers "All owners".
  const loadOwnerOptions = async () => {
    try {
      const users = await listTenantUsers();
      setOwnerOptions(users.map((u) => ({ value: u.id, label: u.display_name || u.username || u.id })));
    } catch {
      setOwnerOptions([]);
    }
  };

  const manageUserAccess = () => {
    handleClose();
    router.push(USER_ACCESS_PATH);
  };

  const confirmAdminRevoke = async () => {
    try {
      setLoading(true);
      const response = await apiUser.deleteTenantToken({ id: adminRevoke.id });
      if (response.errors && response.errors.length > 0) {
        snackbar.error(`Failed to revoke API key - ${parseHttpResponseBodyMessage(response)}`);
        return;
      }
      snackbar.success(`Revoked ${adminRevoke.name}`);
      setAdminRevoke(null);
      // The revoked key may be the admin's own.
      fetchTokens();
      await fetchTenantTokens();
    } finally {
      setLoading(false);
    }
  };

  // Resolves true once the key exists, so the create form knows whether to clear itself.
  const createToken = async ({ name, kind, expiresAt, limits, scopes, gatewayPolicy }) => {
    if (!name) {
      snackbar.error('Token name is required');
      return false;
    }

    try {
      setLoading(true);
      const response = await apiUser.createUserToken({ name, kind, expiresAt, limits, scopes, gatewayPolicy });

      if (response.errors && response.errors.length > 0) {
        snackbar.error(`Failed to create API token - ${parseHttpResponseBodyMessage(response)}`);
      } else if (response.data && response.data.token) {
        setCreatedToken(response.data.token);
        setCreatedTokenName(name);
        setShowCreateDialog(false);
        fetchTokens();
        snackbar.success('API Token created successfully');
        return true;
      } else {
        snackbar.error('Failed to create API token - Invalid response');
      }
    } catch (error) {
      snackbar.error(`Failed to create API token - ${parseHttpResponseBodyMessage(error)}`);
    } finally {
      setLoading(false);
    }
    return false;
  };

  // An empty policy object clears every restriction; the server stores that as NULL.
  const saveGatewayAccess = async (token, gatewayPolicy) => {
    try {
      setSavingAccess(true);
      const response = await apiUser.updateUserToken({ id: token.id, gatewayPolicy });
      if (response.errors && response.errors.length > 0) {
        snackbar.error(`Failed to update key access - ${parseHttpResponseBodyMessage(response)}`);
        return;
      }
      setEditAccessToken(null);
      fetchTokens();
      snackbar.success(`Access updated for ${token.name}`);
    } catch (error) {
      snackbar.error(`Failed to update key access - ${parseHttpResponseBodyMessage(error)}`);
    } finally {
      setSavingAccess(false);
    }
  };

  const revokeToken = (tokenId, tokenName) => {
    setDeleteDialog({ open: true, token: { id: tokenId, name: tokenName } });
  };

  const confirmDeleteToken = async () => {
    const { token } = deleteDialog;
    if (!token) {
      return;
    }

    try {
      setLoading(true);
      const response = await apiUser.deleteUserToken({ id: token.id });
      if (response.errors && response.errors.length > 0) {
        snackbar.error(`Failed to delete API token - ${parseHttpResponseBodyMessage(response)}`);
      } else {
        // Hide the "Token Created Successfully" banner if it refers to the token just deleted.
        if (createdTokenName && token.name === createdTokenName) {
          setCreatedToken(null);
          setCreatedTokenName(null);
        }
        fetchTokens();
        snackbar.success('API Token deleted successfully');
      }
    } catch (error) {
      snackbar.error(`Failed to delete API token - ${parseHttpResponseBodyMessage(error)}`);
    } finally {
      setLoading(false);
      setDeleteDialog({ open: false, token: null });
    }
  };

  const cancelDeleteToken = () => {
    setDeleteDialog({ open: false, token: null });
  };

  const handleClose = () => {
    setShowCreateDialog(false);
    setCreatedToken(null);
    setCreatedTokenName(null);
    setDeleteDialog({ open: false, token: null });
    onClose();
  };

  return (
    <>
      <Modal
        open={open}
        onClose={handleClose}
        title={title}
        subtitle={
          baseTitle
            ? `Keys that let scripts and tools call ${baseTitle} APIs and the AI Gateway.`
            : 'Keys that let scripts and tools call the APIs and the AI Gateway.'
        }
        loader={loading}
        width='lg'
        maxHeight='85vh'
        actionButtons={
          <Box sx={{ display: 'flex', justifyContent: 'space-between', width: '100%' }}>
            <Button
              size='md'
              tone='secondary'
              onClick={() => setShowInstructions(true)}
              icon={<InfoIcon sx={{ fontSize: ds.text.title }} />}
              iconPlacement='start'
            >
              How to use
            </Button>
            <Button tone='secondary' size='md' onClick={handleClose}>
              Close
            </Button>
          </Box>
        }
      >
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[5] }}>
          {/* Created Token Display */}
          {createdToken && (
            <Box
              sx={{
                p: ds.space[4],
                bgcolor: ds.blue[100],
                borderRadius: ds.radius.lg,
                border: `1px solid ${ds.gray[200]}`,
              }}
            >
              <Typography sx={{ fontSize: ds.text.bodyLg, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>
                Token Created Successfully!
              </Typography>
              <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], mb: ds.space[2] }}>
                Please copy this token now. You won&apos;t be able to see it again.
              </Typography>
              <Box
                sx={{
                  p: ds.space[3],
                  bgcolor: ds.background[100],
                  borderRadius: ds.radius.sm,
                  border: `1px solid ${ds.gray[200]}`,
                  fontFamily: ds.font.mono,
                  fontSize: ds.text.bodyLg,
                  wordBreak: 'break-all',
                }}
              >
                {createdToken}
              </Box>
              <Box sx={{ mt: ds.space[3], display: 'flex', justifyContent: 'flex-end' }}>
                <Button
                  size='sm'
                  tone='secondary'
                  onClick={() => {
                    navigator.clipboard.writeText(createdToken);
                    snackbar.success('Token copied to clipboard');
                  }}
                >
                  Copy to Clipboard
                </Button>
              </Box>
            </Box>
          )}

          {/* Tokens List */}
          <Box>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[3], mb: ds.space[4] }}>
              {isKeyAdmin && (
                <ToggleGroup
                  selection='single'
                  size='sm'
                  ariaLabel='Which API keys'
                  value={view}
                  onChange={(next) => {
                    setView(next);
                    if (next === 'all') {
                      fetchTenantTokens(0);
                      if (ownerOptions.length === 0) loadOwnerOptions();
                    }
                  }}
                  options={[
                    { value: 'mine', label: 'My keys' },
                    { value: 'all', label: 'All keys' },
                  ]}
                />
              )}
              {view === 'all' && (
                <>
                  <FilterDropdownButton
                    id='tenant-keys-owner'
                    disablePortal={false}
                    label='Owner'
                    placeholder='All owners'
                    options={ownerOptions}
                    value={tenantFilter.userId || null}
                    onSelect={(e) => changeTenantFilter({ userId: e.target.value || '' })}
                  />
                  <FilterDropdownButton
                    id='tenant-keys-kind'
                    disablePortal={false}
                    label='Type'
                    placeholder='All types'
                    options={KIND_FILTER_OPTIONS}
                    value={tenantFilter.kind || null}
                    onSelect={(e) => changeTenantFilter({ kind: e.target.value || '' })}
                  />
                </>
              )}
              {view === 'mine' && (
                <Box sx={{ ml: 'auto' }}>
                  <Button
                    size='md'
                    onClick={() => {
                      setShowCreateDialog(true);
                      setCreatedToken(null);
                      setCreatedTokenName(null);
                    }}
                  >
                    Create Token
                  </Button>
                </Box>
              )}
            </Box>
            {view === 'mine' && <TokenList tokens={tokens} onRevoke={revokeToken} onEditAccess={setEditAccessToken} />}
            {view === 'all' && tenantError && <Banner tone='critical' message={tenantError} />}
            {view === 'all' && !tenantError && tenantLoaded && (
              <TenantTokenList
                tokens={tenantTokens.tokens}
                total={tenantTokens.total}
                page={tenantPage}
                pageSize={tenantPageSize}
                onPageChange={(next, size) => fetchTenantTokens(next, tenantFilter, size)}
                filtered={Boolean(tenantFilter.userId || tenantFilter.kind)}
                canRevoke={canRevokeAny}
                onRevoke={setAdminRevoke}
                onManageAccess={manageUserAccess}
              />
            )}
          </Box>
        </Box>
      </Modal>

      <CreateTokenDialog open={showCreateDialog} defaultKind={defaultKind} onClose={() => setShowCreateDialog(false)} onCreate={createToken} />
      {editAccessToken && (
        <EditGatewayAccessDialog token={editAccessToken} saving={savingAccess} onClose={() => setEditAccessToken(null)} onSave={saveGatewayAccess} />
      )}

      {/* Instructions Modal */}
      <Modal
        open={showInstructions}
        onClose={() => setShowInstructions(false)}
        width='md'
        title='How to use API Tokens'
        sx={{
          '& .MuiDialog-paper': {
            width: '50%',
            maxWidth: '50%',
          },
        }}
        actionButtons={
          <Button tone='secondary' size='md' onClick={() => setShowInstructions(false)}>
            Close
          </Button>
        }
      >
        <Box sx={{ color: ds.gray[500], fontSize: ds.text.bodyLg, lineHeight: '20px' }}>
          <Typography sx={{ mb: ds.space[3] }}>
            {`API tokens let you authenticate with ${baseTitle} APIs programmatically. Send the token as a Bearer token — no exchange step is needed.`}
          </Typography>

          <Typography sx={{ fontSize: ds.text.title, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>Call the API</Typography>
          <Typography sx={{ mb: ds.space[2] }}>
            {`${baseTitle} APIs speak JSON-RPC 2.0.`} Set <code>method</code> to the action you want and <code>params</code> to its arguments:
          </Typography>
          <Box
            sx={{
              mb: ds.space[4],
              p: ds.space[3],
              bgcolor: ds.blue[100],
              borderRadius: ds.radius.sm,
              fontSize: ds.text.small,
              fontFamily: ds.font.mono,
              border: `1px solid ${ds.gray[200]}`,
              wordBreak: 'break-all',
              overflowX: 'auto',
            }}
          >
            {`curl ${getAppBaseUrl()}/api/rpc -i -H 'content-type: application/json' -H "Authorization: Bearer $NB_API_TOKEN" --data '{"jsonrpc":"2.0","id":1,"method":"accounts_list","params":{}}'`}
          </Box>

          <Box component='ul' sx={{ pl: ds.space.mul(0, 10), mb: ds.space[3] }}>
            <li style={{ marginBottom: ds.space[2] }}>
              <strong>Important:</strong> Send the token exactly as issued, including its <code>sk-nb-</code> prefix
            </li>
            <li style={{ marginBottom: ds.space[2] }}>
              <strong>Current Environment:</strong> Commands above use your current domain ({getAppBaseUrl()})
            </li>
            <li style={{ marginBottom: ds.space[2] }}>
              <strong>Security:</strong> Keep your API tokens secure and never share them publicly
            </li>
            <li>
              <strong>Token Management:</strong> Delete tokens when no longer needed or if compromised
            </li>
          </Box>
        </Box>
      </Modal>

      {/* Delete Confirmation Dialog */}
      <Modal
        open={deleteDialog.open}
        onClose={cancelDeleteToken}
        width='sm'
        title='Delete API Token'
        confirmText='Delete'
        confirmTone='danger'
        onConfirm={confirmDeleteToken}
        loader={loading}
      >
        <Typography sx={{ color: ds.gray[500], fontSize: ds.text.bodyLg, lineHeight: '20px' }}>
          Are you sure you want to delete &quot;{deleteDialog.token?.name}&quot;? This action cannot be undone.
        </Typography>
      </Modal>

      <Modal
        open={!!adminRevoke}
        onClose={() => setAdminRevoke(null)}
        width='sm'
        title='Revoke API key'
        confirmText='Revoke key'
        confirmTone='danger'
        onConfirm={confirmAdminRevoke}
        loader={loading}
      >
        <Typography sx={{ color: ds.gray[500], fontSize: ds.text.bodyLg, lineHeight: '20px' }}>
          &quot;{adminRevoke?.name}&quot; belongs to {adminRevoke?.owner_name || 'another user'}. It stops working within a minute and cannot be
          restored. To limit a user without revoking, use AI Tools → Gateway → User access.
        </Typography>
      </Modal>
    </>
  );
};

export default ApiTokens;
