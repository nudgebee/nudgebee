import React from 'react';
import { Box, Typography } from '@mui/material';
import { Button } from '@ui/Button';
import { Chip } from '@ui/Chip';
import { EmptyState } from '@ui/EmptyState';
import CustomTable from '@shared/tables/CustomTable';
import { ds } from 'src/utils/colors';
import { carriesGatewayTraffic, KIND_LABELS } from './keyKinds';
import { formatDate, KIND_HUES, lastUsedText, suffixText, usageText } from './TokenList';

// Every key in the tenant, for admins. Never shows a secret; revoke only for full admins.
const TenantTokenList = ({ tokens, total, page, pageSize, onPageChange, filtered = false, canRevoke, onRevoke, onManageAccess }) => {
  if (tokens.length === 0) {
    return <EmptyState title={filtered ? 'No API keys match these filters' : 'No API keys in this tenant'} size='section' />;
  }
  const headers = ['Owner', 'Name', 'Key', 'Type', 'Created', 'Expires', 'Last used', 'Last model', 'This month', ''];
  const tableData = tokens.map((t) => {
    const label = KIND_LABELS[t.kind] ?? t.kind;
    return [
      { text: t.owner_name || '—' },
      { text: t.name, component: <Typography variant='body2'>{t.name}</Typography> },
      {
        text: suffixText(t),
        component: (
          <Typography variant='body2' sx={{ fontFamily: ds.font.mono, whiteSpace: 'nowrap' }}>
            {suffixText(t)}
          </Typography>
        ),
      },
      {
        text: label,
        component: (
          <Chip size='xs' variant='tag' hue={KIND_HUES[t.kind]}>
            {label}
          </Chip>
        ),
      },
      { text: formatDate(t.created_at) },
      { text: t.expires_at ? formatDate(t.expires_at) : 'never' },
      { text: lastUsedText(t) },
      { text: t.last_model || '—' },
      { text: carriesGatewayTraffic(t.kind) ? usageText(t.usage) : '—' },
      {
        component: canRevoke ? (
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[2] }}>
            <Button tone='secondary' size='sm' onClick={() => onManageAccess(t)}>
              Manage access
            </Button>
            <Button tone='danger' size='sm' onClick={() => onRevoke(t)}>
              Revoke
            </Button>
          </Box>
        ) : null,
      },
    ];
  });
  // Server-side paging through the table's own pager; page is 0-based here, 1-based in the table.
  return (
    <CustomTable
      headers={headers}
      tableData={tableData}
      rowsPerPage={pageSize}
      totalRows={total}
      pageNumber={page + 1}
      onPageChange={(next, limit) => onPageChange(next - 1, limit)}
    />
  );
};

export default TenantTokenList;
