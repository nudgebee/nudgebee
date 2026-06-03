import React, { useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { Modal } from '@components1/ds/Modal';
import { Button } from '@components1/ds/Button';
import { Input } from '@components1/ds/Input';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { snackbar } from './snackbarService';
import { ds } from 'src/utils/colors';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import InfoIcon from '@mui/icons-material/Info';
import { DeleteIconRed } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import dayjs from 'dayjs';
import apiUser from '@api1/user/';
import { getAppBaseUrl } from '@lib/externalUrls';

const ApiTokens = ({ open, title, onClose }) => {
  const [loading, setLoading] = useState(false);
  const [tokens, setTokens] = useState([]);
  const [showCreateForm, setShowCreateForm] = useState(false);
  const [tokenName, setTokenName] = useState('');
  const [createdToken, setCreatedToken] = useState(null);
  const [showInstructions, setShowInstructions] = useState(false);
  const [deleteDialog, setDeleteDialog] = useState({ open: false, token: null });

  useEffect(() => {
    if (open) {
      fetchTokens();
    }
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

  const createToken = async () => {
    if (!tokenName.trim()) {
      snackbar.error('Token name is required');
      return;
    }

    try {
      setLoading(true);
      const response = await apiUser.createUserToken(tokenName);

      // Check for errors in different response formats
      if (response.errors && response.errors.length > 0) {
        snackbar.error(`Failed to create API token - ${parseHttpResponseBodyMessage(response)}`);
      } else if (response.data && response.data.token) {
        setCreatedToken(response.data.token);
        setTokenName('');
        setShowCreateForm(false);
        fetchTokens();
        snackbar.success('API Token created successfully');
      } else {
        snackbar.error('Failed to create API token - Invalid response');
      }
    } catch (error) {
      snackbar.error(`Failed to create API token - ${parseHttpResponseBodyMessage(error)}`);
    } finally {
      setLoading(false);
    }
  };

  const deleteToken = async (tokenId, tokenName) => {
    setDeleteDialog({ open: true, token: { id: tokenId, name: tokenName } });
  };

  const confirmDeleteToken = async () => {
    const { token } = deleteDialog;
    if (!token) {
      return;
    }

    try {
      setLoading(true);
      const response = await apiUser.deleteUserToken(token.name);
      if (response.errors && response.errors.length > 0) {
        snackbar.error(`Failed to delete API token - ${parseHttpResponseBodyMessage(response)}`);
      } else {
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
    setShowCreateForm(false);
    setTokenName('');
    setCreatedToken(null);
    setDeleteDialog({ open: false, token: null });
    onClose();
  };

  const formatDate = (date) => {
    if (!date) {
      return 'Never';
    }
    return dayjs(date).format('MMM DD, YYYY HH:mm');
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
            maxWidth: '800px',
            '& .MuiDialogContent-root': {
              padding: '32px 40px 0px 40px',
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
                  fontFamily: 'monospace',
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

          {/* Create Token Form */}
          {showCreateForm && (
            <Box
              sx={{
                p: '20px',
                border: `1px solid ${ds.gray[200]}`,
                borderRadius: ds.radius.lg,
                bgcolor: ds.background[100],
              }}
            >
              <Typography sx={{ fontSize: ds.text.title, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[4] }}>
                Create New API Token
              </Typography>
              <Box sx={{ mb: ds.space[4] }}>
                <Input size='sm' label='Token Name' value={tokenName} placeholder='Enter a descriptive name for your token' onChange={setTokenName} />
              </Box>
              <Box sx={{ display: 'flex', gap: ds.space[3], justifyContent: 'flex-end' }}>
                <Button
                  size='md'
                  tone='secondary'
                  onClick={() => {
                    setShowCreateForm(false);
                    setTokenName('');
                  }}
                >
                  Cancel
                </Button>
                <Button size='md' onClick={createToken} disabled={loading || !tokenName.trim()}>
                  Create Token
                </Button>
              </Box>
            </Box>
          )}

          {/* Tokens List */}
          <Box>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: ds.space[4] }}>
              <Typography sx={{ fontSize: ds.text.title, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>API Tokens</Typography>
              {!showCreateForm && (
                <Button
                  size='md'
                  onClick={() => {
                    setShowCreateForm(true);
                    setCreatedToken(null);
                  }}
                >
                  Create New Token
                </Button>
              )}
            </Box>

            {tokens.length === 0 ? (
              <Box
                sx={{
                  textAlign: 'center',
                  py: '40px',
                  color: ds.gray[500],
                }}
              >
                <Typography>No API tokens found. Create your first token to get started.</Typography>
              </Box>
            ) : (
              <CustomTable2
                headers={['Name', 'Created', 'Last Used', 'Actions']}
                tableData={tokens.map((token) => [
                  { text: token.name, component: <Typography variant='body2'>{token.name}</Typography> },
                  { text: formatDate(token.created_at), component: <Typography variant='body2'>{formatDate(token.created_at)}</Typography> },
                  { text: formatDate(token.accessed_at), component: <Typography variant='body2'>{formatDate(token.accessed_at)}</Typography> },
                  {
                    component: (
                      <Button
                        tone='ghost'
                        composition='icon-only'
                        size='sm'
                        onClick={() => deleteToken(token.id, token.name)}
                        icon={<SafeIcon src={DeleteIconRed} alt='delete' width={18} height={18} />}
                        aria-label='Delete token'
                      />
                    ),
                  },
                ])}
              />
            )}
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
              icon={<InfoIcon sx={{ fontSize: '16px' }} />}
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
            API tokens allow you to authenticate with Nudgebee APIs programmatically. Follow this two-step process:
          </Typography>

          <Typography sx={{ fontSize: '15px', fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>
            Step 1: Generate Temporary Token
          </Typography>
          <Typography sx={{ mb: ds.space[2] }}>First, exchange your API token for a temporary JWT token:</Typography>
          <Box
            sx={{
              mb: ds.space[4],
              p: ds.space[3],
              bgcolor: ds.blue[100],
              borderRadius: ds.radius.sm,
              fontSize: ds.text.small,
              fontFamily: 'monospace',
              border: `1px solid ${ds.gray[200]}`,
              wordBreak: 'break-all',
              overflowX: 'auto',
            }}
          >
            {`curl ${getAppBaseUrl()}/api/auth/token --data '{"email":"your@email.com", "secret":"YOUR_API_TOKEN"}' -i -H 'content-type: application/json'`}
          </Box>

          <Typography sx={{ fontSize: '15px', fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[2] }}>
            Step 2: Use Temporary Token for API Calls
          </Typography>
          <Typography sx={{ mb: ds.space[2] }}>Use the JWT token from Step 1 to make GraphQL API calls:</Typography>
          <Box
            sx={{
              mb: ds.space[4],
              p: ds.space[3],
              bgcolor: ds.blue[100],
              borderRadius: ds.radius.sm,
              fontSize: ds.text.small,
              fontFamily: 'monospace',
              border: `1px solid ${ds.gray[200]}`,
              wordBreak: 'break-all',
              overflowX: 'auto',
            }}
          >
            {`curl ${getAppBaseUrl()}/api/graphql -i -H 'content-type: application/json' -H "Authorization: Bearer $AUTH_TOKEN" --data $QUERY_DATA`}
          </Box>

          <Box component='ul' sx={{ pl: '20px', mb: ds.space[3] }}>
            <li style={{ marginBottom: ds.space[2] }}>
              <strong>Important:</strong> Your API token is used as the &quot;secret&quot; in Step 1, not directly in API calls
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
