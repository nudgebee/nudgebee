import React, { useEffect, useState } from 'react';
import {
  Box,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TableRow,
  Typography,
  IconButton,
  Paper,
  Dialog,
  DialogTitle,
  DialogContent,
  DialogActions,
} from '@mui/material';
import { Modal } from './modal';
import CustomButton from './NewCustomButton';
import CustomTextField from './CustomTextField';
import { snackbar } from './snackbarService';
import { colors } from 'src/utils/colors';
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
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: '24px' }}>
          {/* Created Token Display */}
          {createdToken && (
            <Box
              sx={{
                p: '16px',
                bgcolor: colors.background.primaryLightest,
                borderRadius: '8px',
                border: `1px solid ${colors.border.primary}`,
              }}
            >
              <Typography sx={{ fontSize: '14px', fontWeight: 600, color: colors.text.secondary, mb: '8px' }}>Token Created Successfully!</Typography>
              <Typography sx={{ fontSize: '12px', color: colors.text.tertiary, mb: '8px' }}>
                Please copy this token now. You wont be able to see it again.
              </Typography>
              <Box
                sx={{
                  p: '12px',
                  bgcolor: colors.background.white,
                  borderRadius: '4px',
                  border: `1px solid ${colors.border.primary}`,
                  fontFamily: 'monospace',
                  fontSize: '14px',
                  wordBreak: 'break-all',
                }}
              >
                {createdToken}
              </Box>
              <Box sx={{ mt: '12px', display: 'flex', justifyContent: 'flex-end' }}>
                <CustomButton
                  size='Small'
                  text='Copy to Clipboard'
                  onClick={() => {
                    navigator.clipboard.writeText(createdToken);
                    snackbar.success('Token copied to clipboard');
                  }}
                  variant='secondary'
                />
              </Box>
            </Box>
          )}

          {/* Create Token Form */}
          {showCreateForm && (
            <Box
              sx={{
                p: '20px',
                border: `1px solid ${colors.border.primary}`,
                borderRadius: '8px',
                bgcolor: colors.background.white,
              }}
            >
              <Typography sx={{ fontSize: '16px', fontWeight: 600, color: colors.text.secondary, mb: '16px' }}>Create New API Token</Typography>
              <CustomTextField
                label='Token Name'
                value={tokenName}
                fullWidth={true}
                placeholder='Enter a descriptive name for your token'
                onChange={(e) => setTokenName(e.target.value)}
                sx={{ mb: '16px' }}
              />
              <Box sx={{ display: 'flex', gap: '12px', justifyContent: 'flex-end' }}>
                <CustomButton
                  size='Medium'
                  text='Cancel'
                  variant='secondary'
                  onClick={() => {
                    setShowCreateForm(false);
                    setTokenName('');
                  }}
                />
                <CustomButton size='Medium' text='Create Token' onClick={createToken} disabled={loading || !tokenName.trim()} />
              </Box>
            </Box>
          )}

          {/* Tokens List */}
          <Box>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', mb: '16px' }}>
              <Typography sx={{ fontSize: '16px', fontWeight: 600, color: colors.text.secondary }}>API Tokens</Typography>
              {!showCreateForm && (
                <CustomButton
                  size='Medium'
                  text='Create New Token'
                  onClick={() => {
                    setShowCreateForm(true);
                    setCreatedToken(null);
                  }}
                />
              )}
            </Box>

            {tokens.length === 0 ? (
              <Box
                sx={{
                  textAlign: 'center',
                  py: '40px',
                  color: colors.text.tertiary,
                }}
              >
                <Typography>No API tokens found. Create your first token to get started.</Typography>
              </Box>
            ) : (
              <TableContainer component={Paper} sx={{ boxShadow: 'none', border: `1px solid ${colors.border.primary}` }}>
                <Table>
                  <TableHead>
                    <TableRow sx={{ bgcolor: colors.background.primaryLightest }}>
                      <TableCell sx={{ fontWeight: 600, color: colors.text.secondary }}>Name</TableCell>
                      <TableCell sx={{ fontWeight: 600, color: colors.text.secondary }}>Created</TableCell>
                      <TableCell sx={{ fontWeight: 600, color: colors.text.secondary }}>Last Used</TableCell>
                      <TableCell sx={{ fontWeight: 600, color: colors.text.secondary, width: '100px' }}>Actions</TableCell>
                    </TableRow>
                  </TableHead>
                  <TableBody>
                    {tokens.map((token) => (
                      <TableRow key={token.id} sx={{ '&:hover': { bgcolor: colors.background.primaryLightest } }}>
                        <TableCell sx={{ color: colors.text.secondary, fontWeight: 500 }}>{token.name}</TableCell>
                        <TableCell sx={{ color: colors.text.tertiary, fontSize: '14px' }}>{formatDate(token.created_at)}</TableCell>
                        <TableCell sx={{ color: colors.text.tertiary, fontSize: '14px' }}>{formatDate(token.accessed_at)}</TableCell>
                        <TableCell>
                          <IconButton
                            size='small'
                            onClick={() => deleteToken(token.id, token.name)}
                            sx={{
                              color: colors.error?.main || '#f44336',
                              '&:hover': {
                                bgcolor: colors.error?.light || '#ffebee',
                              },
                            }}
                          >
                            <SafeIcon src={DeleteIconRed} alt='delete' width={18} height={18} />
                          </IconButton>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </TableContainer>
            )}
          </Box>

          {/* Action Buttons */}
          <Box
            sx={{
              display: 'flex',
              justifyContent: 'space-between',
              gap: '12px',
              pt: '16px',
              borderTop: `1px solid ${colors.border.primary}`,
              mt: '24px',
              mb: '8px',
            }}
          >
            <CustomButton
              size='Small'
              text='How to use'
              variant='secondary'
              onClick={() => setShowInstructions(true)}
              startIcon={<InfoIcon sx={{ fontSize: '16px' }} />}
            />
            <CustomButton variant='secondary' size='Medium' onClick={handleClose} text='Close' sx={{ padding: '8px 16px' }} />
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
        footerContent={<CustomButton text='Close' variant='secondary' size='Medium' onClick={() => setShowInstructions(false)} />}
      >
        <Box sx={{ color: colors.text.tertiary, fontSize: '14px', lineHeight: '20px' }}>
          <Typography sx={{ mb: '12px' }}>
            API tokens allow you to authenticate with Nudgebee APIs programmatically. Follow this two-step process:
          </Typography>

          <Typography sx={{ fontSize: '15px', fontWeight: 600, color: colors.text.secondary, mb: '8px' }}>
            Step 1: Generate Temporary Token
          </Typography>
          <Typography sx={{ mb: '8px' }}>First, exchange your API token for a temporary JWT token:</Typography>
          <Box
            sx={{
              mb: '16px',
              p: '12px',
              bgcolor: colors.background.primaryLightest,
              borderRadius: '4px',
              fontSize: '12px',
              fontFamily: 'monospace',
              border: `1px solid ${colors.border.primary}`,
              wordBreak: 'break-all',
              overflowX: 'auto',
            }}
          >
            {`curl ${getAppBaseUrl()}/api/auth/token --data '{"email":"your@email.com", "secret":"YOUR_API_TOKEN"}' -i -H 'content-type: application/json'`}
          </Box>

          <Typography sx={{ fontSize: '15px', fontWeight: 600, color: colors.text.secondary, mb: '8px' }}>
            Step 2: Use Temporary Token for API Calls
          </Typography>
          <Typography sx={{ mb: '8px' }}>Use the JWT token from Step 1 to make GraphQL API calls:</Typography>
          <Box
            sx={{
              mb: '16px',
              p: '12px',
              bgcolor: colors.background.primaryLightest,
              borderRadius: '4px',
              fontSize: '12px',
              fontFamily: 'monospace',
              border: `1px solid ${colors.border.primary}`,
              wordBreak: 'break-all',
              overflowX: 'auto',
            }}
          >
            {`curl ${getAppBaseUrl()}/api/graphql -i -H 'content-type: application/json' -H "Authorization: Bearer $AUTH_TOKEN" --data $QUERY_DATA`}
          </Box>

          <Box component='ul' sx={{ pl: '20px', mb: '12px' }}>
            <li style={{ marginBottom: '8px' }}>
              <strong>Important:</strong> Your API token is used as the &quot;secret&quot; in Step 1, not directly in API calls
            </li>
            <li style={{ marginBottom: '8px' }}>
              <strong>Current Environment:</strong> Commands above use your current domain ({getAppBaseUrl()})
            </li>
            <li style={{ marginBottom: '8px' }}>
              <strong>Security:</strong> Keep your API tokens secure and never share them publicly
            </li>
            <li>
              <strong>Token Management:</strong> Delete tokens when no longer needed or if compromised
            </li>
          </Box>
        </Box>
      </Modal>

      {/* Delete Confirmation Dialog */}
      <Dialog
        open={deleteDialog.open}
        onClose={cancelDeleteToken}
        maxWidth='sm'
        fullWidth
        PaperProps={{
          sx: {
            borderRadius: '8px',
          },
        }}
      >
        <DialogTitle sx={{ fontSize: '18px', fontWeight: 600, color: colors.text.secondary, pb: '8px' }}>Delete API Token</DialogTitle>
        <DialogContent sx={{ pb: '16px' }}>
          <Typography sx={{ color: colors.text.tertiary, fontSize: '14px', lineHeight: '20px' }}>
            Are you sure you want to delete &quot;{deleteDialog.token?.name}&quot;? This action cannot be undone.
          </Typography>
        </DialogContent>
        <DialogActions sx={{ p: '16px 24px 24px 24px', gap: '12px' }}>
          <CustomButton variant='secondary' size='Medium' text='Cancel' onClick={cancelDeleteToken} />
          <CustomButton size='Medium' text='Delete' onClick={confirmDeleteToken} />
        </DialogActions>
      </Dialog>
    </>
  );
};

export default ApiTokens;
