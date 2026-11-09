import React from 'react';
import { Box, Stack } from '@mui/material';
import { Chip } from '@ui/Chip';
import { ds } from '@utils/colors';
import ProvidersTable from './ProvidersTable';

/**
 * LLM configuration v2 (LLM_CONFIG_V2 flag) — Phase 1 shows the
 * provider-grouped read-only view; the account-mapping step arrives with
 * Phase 2, which is why step 2 renders muted. Editing still happens on
 * Admin → Integrations → LLM until the new forms land.
 */
const LLMConfigurationScreen = ({ stickyTable = false }) => (
  <Box>
    <Stack direction='row' spacing={1} alignItems='center' useFlexGap flexWrap='wrap' sx={{ mb: ds.space[3] }}>
      <Chip size='sm' variant='tag' tone='info'>
        1 · Add your provider and select its models
      </Chip>
      <Box component='span' sx={{ color: ds.gray[500] }}>
        →
      </Box>
      <Chip size='sm' variant='tag' tone='neutral'>
        2 · Map the relevant models to your account (coming soon)
      </Chip>
    </Stack>
    <ProvidersTable stickyTable={stickyTable} />
  </Box>
);

export default LLMConfigurationScreen;
