import React, { useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { Button } from '@ui/Button';
import { toast as snackbar } from '@ui/Toast';
import { ds } from 'src/utils/colors';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import InfoIcon from '@mui/icons-material/Info';
import apiUser from '@api1/user/';
import { getAppBaseUrl } from '@lib/externalUrls';
import TokenList from './TokenList';
import CreateTokenDialog from './CreateTokenDialog';

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

  useEffect(() => {
    if (open) {
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

  // Resolves true once the key exists, so the create form knows whether to clear itself.
  const createToken = async ({ name, kind, expiresAt, limits, scopes }) => {
    if (!name) {
      snackbar.error('Token name is required');
      return false;
    }

    try {
      setLoading(true);
      const response = await apiUser.createUserToken({ name, kind, expiresAt, limits, scopes });

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
        loader={loading}
        width='md'
        sx={{
          '& .MuiPaper-root': {
            // Wide enough for every kind's columns in one table (it was 800px when each tab held one kind).
            maxWidth: ds.space.mul(0, 550),
            '& .MuiDialogContent-root': {
              padding: `${ds.space[6]} ${ds.space.mul(0, 20)} 0px ${ds.space.mul(0, 20)}`,
            },
          },
        }}
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
            <Box sx={{ display: 'flex', justifyContent: 'flex-end', alignItems: 'center', mb: ds.space[4] }}>
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

            <TokenList tokens={tokens} onRevoke={revokeToken} />
          </Box>

          {/* Action Buttons */}
          <Box
            sx={{
              display: 'flex',
              justifyContent: 'space-between',
              gap: ds.space[3],
              pt: ds.space[4],
              borderTop: `1px solid ${ds.gray[200]}`,
              mt: ds.space[5],
              mb: ds.space[2],
            }}
          >
            <Button
              size='sm'
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
        </Box>
      </Modal>

      <CreateTokenDialog open={showCreateDialog} defaultKind={defaultKind} onClose={() => setShowCreateDialog(false)} onCreate={createToken} />

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
    </>
  );
};

export default ApiTokens;
