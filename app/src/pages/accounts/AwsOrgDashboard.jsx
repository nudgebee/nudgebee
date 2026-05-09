import { Grid, Typography, Box, IconButton, Tooltip, Chip, Stack } from '@mui/material';
import RefreshIcon from '@mui/icons-material/Refresh';
import { useState, useEffect } from 'react';
import apiAccount from '@api1/account';
import CustomButton from '@components1/common/NewCustomButton';
import { snackbar } from '@components1/common/snackbarService';
import { BoxLayout2 } from '@components1/common';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import Text from '@components1/common/format/Text';
import Datetime from '@components1/common/format/Datetime';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { Modal } from '@components1/common/modal';
import { colors } from 'src/utils/colors';
import { CopyIconBlue } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';

const STATUS_COLORS = {
  active: '#4CAF50',
  pending: '#FF9800',
  disabled: '#9E9E9E',
  failed: '#F44336',
};

const TABLE_HEADERS = ['Account Number', 'Name', 'Status', 'Created At'];

const AwsOrgDashboard = () => {
  const [loading, setLoading] = useState(true);
  const [orgName, setOrgName] = useState('');
  const [orgStatus, setOrgStatus] = useState('');
  const [memberAccounts, setMemberAccounts] = useState([]);
  const [tableData, setTableData] = useState([]);
  const [refreshTokenModalOpen, setRefreshTokenModalOpen] = useState(false);
  const [newToken, setNewToken] = useState('');
  const [isRefreshing, setIsRefreshing] = useState(false);

  const fetchOrgStatus = () => {
    setLoading(true);
    apiAccount
      .awsOrgStatus()
      .then((res) => {
        const data = res?.data?.aws_org_status;
        if (data) {
          setOrgName(data.org_name || '');
          setOrgStatus(data.org_status || '');
          setMemberAccounts(data.member_accounts || []);
          const rows = (data.member_accounts || []).map((account) => [
            { component: <Text value={account.account_number} /> },
            { component: <Text value={account.account_name} /> },
            { component: <CustomLabels text={account.status || '-'} /> },
            { component: <Datetime value={account.created_at} /> },
          ]);
          setTableData(rows);
        }
      })
      .catch(() => {
        setOrgName('');
        setOrgStatus('');
        setMemberAccounts([]);
        setTableData([]);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    fetchOrgStatus();
  }, []);

  const handleRefreshToken = () => {
    setIsRefreshing(true);
    apiAccount
      .awsOrgRefreshToken()
      .then((res) => {
        const data = res?.data?.aws_org_refresh_token;
        if (data?.verification_token) {
          setNewToken(data.verification_token);
          setRefreshTokenModalOpen(true);
          snackbar.success('Verification token regenerated.');
        } else {
          const errorMsg = res?.errors?.[0]?.message || 'Failed to regenerate token';
          snackbar.error(errorMsg);
        }
      })
      .catch(() => {
        snackbar.error('Failed to regenerate verification token');
      })
      .finally(() => {
        setIsRefreshing(false);
      });
  };

  const handleCopyToken = () => {
    navigator.clipboard.writeText(newToken);
    snackbar.success('Token copied to clipboard');
  };

  if (!loading && !orgName) {
    return (
      <Box
        sx={{
          mt: 2,
          mx: 'auto',
          width: '100%',
          maxWidth: 900,
          backgroundColor: '#fff',
          borderRadius: '12px',
          boxShadow: '0 1px 3px rgba(0,0,0,0.08)',
          p: 2,
          textAlign: 'center',
        }}
      >
        <Typography variant='h6' sx={{ fontWeight: 600, mb: 1 }}>
          No AWS Organization Found
        </Typography>

        <Typography variant='body2' sx={{ color: 'text.secondary', mb: 3 }}>
          You haven't onboarded an AWS Organization yet.
        </Typography>
      </Box>
    );
  }
  return (
    <>
      {/* Refresh Token Modal */}
      <Modal width='sm' open={refreshTokenModalOpen} handleClose={() => setRefreshTokenModalOpen(false)} title='New Verification Token'>
        <Box sx={{ px: 3, pt: 2, pb: 1 }}>
          <Typography sx={{ fontSize: '20px', fontWeight: 500, color: '#374151', mb: 1 }}>Verification Token</Typography>
          <Typography variant='body2' sx={{ color: colors.secondary.dark, fontSize: '12px', mb: 2 }}>
            Copy this token now — it will not be shown again. Use it as a StackSet parameter.
          </Typography>

          <Box
            sx={{
              backgroundColor: '#2F2F2F',
              padding: 1.5,
              borderRadius: 1,
              overflowX: 'auto',
            }}
          >
            <Typography variant='body2' sx={{ fontSize: '14px', color: 'white', fontFamily: 'monospace', wordBreak: 'break-all' }}>
              {newToken}
            </Typography>

            <Box sx={{ display: 'flex', gap: 1, mt: 1 }}>
              <CustomButton
                size='Small'
                text='Copy Token'
                startIcon={<SafeIcon src={CopyIconBlue} alt='copy token' height={16} width={16} />}
                variant='tertiary'
                onClick={handleCopyToken}
                sx={{ fontSize: '12px' }}
              />
            </Box>
          </Box>
        </Box>
        <Grid container spacing={2} mt={1} mb={2} justifyContent='flex-end' sx={{ pr: 3 }}>
          <Grid item>
            <CustomButton size='Medium' text='Close' onClick={() => setRefreshTokenModalOpen(false)} />
          </Grid>
        </Grid>
      </Modal>

      {/* Dashboard Header */}
      <Grid container padding='5px' mt={4}>
        <Grid item xs={12}>
          <Stack direction='row' alignItems='center' justifyContent='space-between'>
            <Stack direction='row' alignItems='center' spacing={1}>
              <Typography color='text.secondary' fontSize='16px' fontWeight={600}>
                AWS Organization: {orgName}
              </Typography>
              <Chip
                label={orgStatus}
                size='small'
                sx={{
                  bgcolor: STATUS_COLORS[orgStatus] || '#9E9E9E',
                  color: '#fff',
                  fontWeight: 600,
                  fontSize: '11px',
                }}
              />
            </Stack>
            <Stack direction='row' spacing={1}>
              <CustomButton size='Medium' text='Regenerate Token' variant='secondary' loading={isRefreshing} onClick={handleRefreshToken} />
              <Tooltip title='Refresh status'>
                <IconButton onClick={fetchOrgStatus}>
                  <RefreshIcon />
                </IconButton>
              </Tooltip>
            </Stack>
          </Stack>
          <Typography variant='body2' sx={{ color: colors.secondary.dark, mt: 0.5 }}>
            {memberAccounts.length} member account{memberAccounts.length !== 1 ? 's' : ''} registered via organization onboarding
          </Typography>
        </Grid>
      </Grid>

      {/* Pending setup banner */}
      {orgStatus === 'pending' && memberAccounts.length === 0 && (
        <Box
          sx={{
            mt: 2,
            mx: '5px',
            p: 1.5,
            backgroundColor: '#f8f9fa',
            borderRadius: '8px',
            border: '1px solid #dee2e6',
          }}
        >
          <Typography variant='body2' sx={{ fontWeight: 600, fontSize: '13px', color: '#374151' }}>
            Setup incomplete — deploy the StackSet in your AWS Management Account to start registering member accounts.
          </Typography>
          <Typography variant='body2' sx={{ color: colors.secondary.dark, fontSize: '12px', mt: 0.5 }}>
            Re-open the AWS Organization onboarding to get the StackSet template and parameters. If you need a new verification token, click
            &quot;Regenerate Token&quot; above.
          </Typography>
        </Box>
      )}

      <BoxLayout2 id='aws-org-members' loading={loading} sharingOptions={false}>
        <CustomTable2 loading={loading} tableData={tableData} headers={TABLE_HEADERS} totalRows={tableData.length} rowsPerPage={tableData.length} />
      </BoxLayout2>
    </>
  );
};

export default AwsOrgDashboard;
