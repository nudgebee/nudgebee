import { Typography, Box, Stack } from '@mui/material';
import { Chip } from '@components1/ds/Chip';
import Datetime from '@common-new/format/Datetime';
import Text from '@common-new/format/Text';
import RefreshIcon from '@mui/icons-material/Refresh';
import { useState, useEffect } from 'react';
import apiAccount from '@api1/account';
import { Button } from '@components1/ds/Button';
import { ListingLayout } from '@components1/ds/ListingLayout';
import { toast as snackbar } from '@components1/ds/Toast';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { Label } from '@components1/ds/Label';
import { Modal } from '@components1/ds/Modal';
import { CopyIconBlue } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { hasWriteAccess } from '@lib/auth';

const STATUS_TONES = {
  active: 'success',
  pending: 'warning',
  disabled: 'neutral',
  failed: 'critical',
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
            { component: <Label text={account.status || '-'} /> },
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
          mt: 'var(--ds-space-4)',
          mx: 'auto',
          width: '100%',
          maxWidth: 900,
          backgroundColor: 'var(--ds-background-100)',
          borderRadius: 'var(--ds-radius-md)',
          border: '1px solid var(--ds-gray-200)',
          p: 'var(--ds-space-4)',
          textAlign: 'center',
        }}
      >
        <Typography
          sx={{
            fontSize: 'var(--ds-text-title)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            lineHeight: 'var(--ds-text-title-lh)',
            mb: 'var(--ds-space-2)',
          }}
        >
          No AWS Organization Found
        </Typography>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body-lg)',
            lineHeight: 'var(--ds-text-body-lg-lh)',
            color: 'var(--ds-gray-500)',
            mb: 'var(--ds-space-5)',
          }}
        >
          You haven&apos;t onboarded an AWS Organization yet.
        </Typography>
      </Box>
    );
  }
  return (
    <>
      <Modal width='sm' open={refreshTokenModalOpen} handleClose={() => setRefreshTokenModalOpen(false)} title='New Verification Token'>
        <Box sx={{ px: 'var(--ds-space-5)', pt: 'var(--ds-space-4)', pb: 'var(--ds-space-2)' }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-heading)',
              fontWeight: 'var(--ds-font-weight-medium)',
              color: 'var(--ds-gray-700)',
              mb: 'var(--ds-space-2)',
            }}
          >
            Verification Token
          </Typography>
          <Typography
            sx={{
              color: 'var(--ds-gray-500)',
              fontSize: 'var(--ds-text-small)',
              mb: 'var(--ds-space-4)',
            }}
          >
            Copy this token now — it will not be shown again. Use it as a StackSet parameter.
          </Typography>

          <Box
            sx={{
              backgroundColor: 'var(--ds-gray-700)',
              padding: 'var(--ds-space-3)',
              borderRadius: 'var(--ds-radius-sm)',
              overflowX: 'auto',
            }}
          >
            <Typography
              sx={{
                fontSize: 'var(--ds-text-body-lg)',
                color: 'var(--ds-background-100)',
                fontFamily: 'var(--ds-font-mono)',
                wordBreak: 'break-all',
              }}
            >
              {newToken}
            </Typography>

            <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', mt: 'var(--ds-space-2)' }}>
              <Button
                tone='secondary'
                size='sm'
                icon={<SafeIcon src={CopyIconBlue} alt='copy token' height={16} width={16} />}
                onClick={handleCopyToken}
                data-testid='copy-token-btn'
              >
                Copy Token
              </Button>
            </Box>
          </Box>
        </Box>
        <Box
          sx={{
            display: 'flex',
            justifyContent: 'flex-end',
            mt: 'var(--ds-space-2)',
            mb: 'var(--ds-space-4)',
            pr: 'var(--ds-space-5)',
          }}
        >
          <Button tone='secondary' size='md' onClick={() => setRefreshTokenModalOpen(false)} data-testid='close-token-modal-btn'>
            Close
          </Button>
        </Box>
      </Modal>

      <ListingLayout id='aws-org-members' sx={{ mt: 'var(--ds-space-6)' }}>
        <ListingLayout.Toolbar
          actions={
            <Stack direction='row' spacing={1}>
              {hasWriteAccess() && (
                <Button tone='primary' size='md' loading={isRefreshing} onClick={handleRefreshToken} data-testid='regenerate-token-btn'>
                  Regenerate Token
                </Button>
              )}
              <Button
                tone='secondary'
                size='md'
                tooltip='Refresh status'
                tooltipPlacement='top'
                tooltipDisableFlip
                composition='icon-only'
                icon={<RefreshIcon fontSize='small' />}
                aria-label='Refresh status'
                onClick={fetchOrgStatus}
                data-testid='refresh-status-btn'
              />
            </Stack>
          }
        >
          <Stack direction='column' spacing={0.5} sx={{ minWidth: 0 }}>
            <Stack direction='row' alignItems='center' spacing={1}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-title)',
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  color: 'var(--ds-gray-700)',
                }}
              >
                AWS Organization: {orgName}
              </Typography>
              <Chip variant='status' size='sm' tone={STATUS_TONES[orgStatus] || 'neutral'} dot>
                {orgStatus}
              </Chip>
            </Stack>
            <Typography sx={{ color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-body-lg)' }}>
              {memberAccounts.length} member account{memberAccounts.length !== 1 ? 's' : ''} registered via organization onboarding
            </Typography>
          </Stack>
        </ListingLayout.Toolbar>

        <ListingLayout.Body>
          {orgStatus === 'pending' && memberAccounts.length === 0 && (
            <Box
              sx={{
                m: 'var(--ds-space-4)',
                p: 'var(--ds-space-3)',
                backgroundColor: 'var(--ds-background-200)',
                borderRadius: 'var(--ds-radius-md)',
                border: '1px solid var(--ds-gray-200)',
              }}
            >
              <Typography
                sx={{
                  fontWeight: 'var(--ds-font-weight-semibold)',
                  fontSize: 'var(--ds-text-body)',
                  color: 'var(--ds-gray-700)',
                }}
              >
                Setup incomplete — deploy the StackSet in your AWS Management Account to start registering member accounts.
              </Typography>
              <Typography
                sx={{
                  color: 'var(--ds-gray-500)',
                  fontSize: 'var(--ds-text-small)',
                  mt: 'var(--ds-space-1)',
                }}
              >
                Re-open the AWS Organization onboarding to get the StackSet template and parameters. If you need a new verification token, click
                &quot;Regenerate Token&quot; above.
              </Typography>
            </Box>
          )}
          <CustomTable2 loading={loading} tableData={tableData} headers={TABLE_HEADERS} totalRows={tableData.length} rowsPerPage={tableData.length} />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

export default AwsOrgDashboard;
