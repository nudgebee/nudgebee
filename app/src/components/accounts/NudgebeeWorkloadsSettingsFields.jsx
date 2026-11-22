import { Box, Typography } from '@mui/material';
import Heading from '@components/common/Heading';
import { Checkbox } from '@ui/Checkbox';
import { Input } from '@ui/Input';
import { useBrandingConfig } from '@hooks/useTenantBranding';
import { ds } from 'src/utils/colors';

// Fields for the per-account `nudgebee_workloads` setting: alerts and digest
// emails leave out the agent's namespace (reported by the agent, shown here
// read-only) and any namespaces listed. The app still shows those workloads.
const NudgebeeWorkloadsSettingsFields = ({ form, onChange, agentNamespace, disabled = false }) => {
  const { title } = useBrandingConfig();
  const set = (key) => (value) => onChange({ ...form, [key]: value });
  const fieldsDisabled = !form.hideInNotifications || disabled;
  return (
    <>
      <Heading value={title ? `${title}'s Own Workloads` : 'Own Workloads'} borderWidth='md' />
      <Box display='flex' flexDirection='column' gap={ds.space[3]} sx={{ mt: ds.space[3] }}>
        <Checkbox
          size='sm'
          checked={form.hideInNotifications}
          label={title ? `Keep ${title}'s own workloads out of alerts and digest emails` : 'Keep our own workloads out of alerts and digest emails'}
          onChange={set('hideInNotifications')}
          disabled={disabled}
          data-testid='nudgebee-workloads-hide'
        />
        <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500] }}>
          They still show in the app. Agent namespace:{' '}
          <Box component='span' sx={{ fontFamily: ds.font.mono, color: ds.gray[700] }} data-testid='nudgebee-workloads-agent-namespace'>
            {agentNamespace || 'not reported yet'}
          </Box>
        </Typography>
        <Input
          value={form.namespaces}
          label={title ? `Other ${title} Namespaces` : 'Other Namespaces'}
          help={
            title
              ? `Comma-separated. Add the namespace the ${title} server runs in, if this cluster hosts it.`
              : 'Comma-separated. Add the namespace the server runs in, if this cluster hosts it.'
          }
          placeholder='e.g. nudgebee'
          onChange={set('namespaces')}
          disabled={fieldsDisabled}
          data-testid='nudgebee-workloads-namespaces'
        />
      </Box>
    </>
  );
};

export default NudgebeeWorkloadsSettingsFields;
