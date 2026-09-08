import { useEffect, useMemo, useState } from 'react';
import PropTypes from 'prop-types';
import { Box, Typography } from '@mui/material';
import { Modal } from '@ui/Modal';
import { Select } from '@ui/Select';
import CloudProviderIcon from '@components/common/icons/CloudProviderIcon';
import Loader from '@shared/Loader';
import { queryGraphQL } from '@lib/HttpService';
import { ds } from '@utils/colors';

// Shared "pick an account" prompt for surfaces that are account-scoped but can
// be reached without an account in context — b-Cortex opened from the global
// sidebar, the Settings memory tab, and so on. Extracted from BCortexModal so
// the failure handling below lives in one place; it lives under components/
// rather than ee/ because OSS surfaces need it too and OSS cannot import @ee.
const LIST_ACCOUNTS_FOR_PICKER = `
query AccountPickerAccounts {
  cloud_accounts: accounts_list(order_by: [{column: "account_name", order: asc}]) {
    rows {
      id
      account_name
      cloud_provider
    }
  }
}`;

const AccountPickerModal = ({ open, onSelect, onCancel, title, description, testIdPrefix }) => {
  const [rows, setRows] = useState(null);
  const [error, setError] = useState(null);
  // Controlled Select value — reset on every open so a re-open never flashes
  // a stale selection while the parent unmounts. Fires onSelect *after* the
  // local state moves so the Select re-renders with the chosen value visible
  // for the instant before the picker closes.
  const [selectedValue, setSelectedValue] = useState('');

  useEffect(() => {
    if (!open) return undefined;
    let cancelled = false;
    setRows(null);
    setError(null);
    setSelectedValue('');
    queryGraphQL(LIST_ACCOUNTS_FOR_PICKER, 'AccountPickerAccounts', {})
      .then((response) => {
        if (cancelled) return;
        // queryGraphQL resolves (not rejects) for BOTH GraphQL-level errors
        // AND network / HTTP failures — the shared axios wrapper swallows
        // them and returns `e.response` / `e.request`. Distinguish three
        // failure modes so none silently render as "no accessible cloud
        // accounts":
        //   1. HTTP error   → response.status >= 400
        //   2. Missing body → response.data absent (network layer error)
        //   3. GraphQL error→ response.data.errors non-empty
        if (response?.status && response.status >= 400) {
          setError(`Failed to load accounts (HTTP ${response.status})`);
          return;
        }
        if (!response?.data) {
          setError('Failed to load accounts (no response body)');
          return;
        }
        const errors = response.data.errors;
        if (errors && errors.length > 0) {
          setError(errors[0]?.message || 'Failed to load accounts');
          return;
        }
        // Trust the server's scoping. `accounts_list` already returns only
        // what this user is authorised to see (tenant + permission gates
        // are applied server-side). No additional client-side filter — an
        // earlier attempt using `session.accountIds` broke super-admins,
        // whose session field is legitimately empty because their access
        // is wildcard rather than an explicit id list.
        setRows(response.data.data?.cloud_accounts?.rows ?? []);
      })
      .catch((err) => {
        if (cancelled) return;
        setError(err?.message || 'Failed to load accounts');
      });
    return () => {
      cancelled = true;
    };
  }, [open]);

  // Map rows → Select option shape, tagging each option with its raw provider
  // so the picker renders under collapsible section headers ("AWS", "GCP", …).
  // `grouped` + `groupIcon` on the ds Select place the provider glyph on the
  // group header itself (matches the LLM Config Accounts picker exactly);
  // per-option icons are omitted so rows stay clean.
  const options = useMemo(
    () =>
      (rows ?? []).map((r) => ({
        value: r.id,
        label: r.account_name || r.id,
        group: r.cloud_provider || 'Other',
      })),
    [rows]
  );

  return (
    <Modal width='sm' title={title} open={open} handleClose={onCancel} onClose={onCancel} maxHeight='auto'>
      <Box sx={{ padding: `0 ${ds.space[6]} ${ds.space[6]}` }} data-testid={testIdPrefix}>
        <Typography sx={{ color: ds.gray[700], fontSize: ds.text.small, mb: ds.space[3] }}>{description}</Typography>
        {error && (
          <Typography sx={{ color: ds.red[600], fontSize: ds.text.small }} data-testid={`${testIdPrefix}-error`}>
            {error}
          </Typography>
        )}
        {!error && rows === null && <Loader style={{ position: 'static', height: 60 }} />}
        {!error && rows !== null && rows.length === 0 && (
          <Typography sx={{ color: ds.gray[600], fontSize: ds.text.small }} data-testid={`${testIdPrefix}-empty`}>
            No accessible cloud accounts. Ask an admin to grant access, or open this from an account page.
          </Typography>
        )}
        {!error && rows !== null && rows.length > 0 && (
          <Select
            label='Account'
            placeholder='Select an account'
            options={options}
            value={selectedValue}
            onChange={(next) => {
              setSelectedValue(next);
              onSelect(next);
            }}
            required
            clearable={false}
            grouped
            groupIcon={(provider) =>
              // CloudProviderIcon falls back to the AWS glyph for any
              // unknown provider — including the "Other" bucket for rows
              // with no cloud_provider — which would mis-label those
              // groups. Suppress the icon entirely for that bucket.
              provider === 'Other' ? null : <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />
            }
            data-testid={`${testIdPrefix}-select`}
          />
        )}
      </Box>
    </Modal>
  );
};

AccountPickerModal.propTypes = {
  open: PropTypes.bool.isRequired,
  onSelect: PropTypes.func.isRequired,
  onCancel: PropTypes.func.isRequired,
  title: PropTypes.string.isRequired,
  description: PropTypes.string.isRequired,
  testIdPrefix: PropTypes.string,
};

AccountPickerModal.defaultProps = {
  testIdPrefix: 'account-picker',
};

export default AccountPickerModal;
