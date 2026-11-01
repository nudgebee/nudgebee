import { Box } from '@mui/material';
import { Checkbox } from '@ui/Checkbox';
import { Input } from '@ui/Input';
import { ds } from 'src/utils/colors';

// Fields for the per-account `event_analysis` setting. The namespace fields
// match Kubernetes event namespaces, so they are shown for Kubernetes accounts
// only (showNamespaces).
const EventAnalysisSettingsFields = ({ form, onChange, showNamespaces = false, disabled = false }) => {
  const set = (key) => (value) => onChange({ ...form, [key]: value });
  return (
    <Box display='flex' flexDirection='column' gap={ds.space[3]}>
      <Checkbox
        size='sm'
        checked={form.reuseRelated}
        label='Reuse the analysis of a recent similar event'
        onChange={set('reuseRelated')}
        disabled={disabled}
        data-testid='event-analysis-reuse-related'
      />
      <Input
        value={form.reuseWindowHours}
        label='Reuse Window (hours)'
        help='How long a finished analysis is reused, for similar events and when reopening the same event. Leave blank for the platform default.'
        placeholder='Platform default'
        type='number'
        inputMode='numeric'
        onChange={set('reuseWindowHours')}
        onKeyDown={(e) => {
          if (e.key === '-') {
            e.preventDefault();
          }
        }}
        disabled={disabled}
        data-testid='event-analysis-reuse-window'
      />
      {showNamespaces && (
        <>
          <Input
            value={form.alwaysFreshNamespaces}
            label='Always-Fresh Namespaces'
            help='Comma-separated. New events in these namespaces always get their own analysis, even when reuse is on.'
            placeholder='e.g. demo'
            onChange={set('alwaysFreshNamespaces')}
            disabled={!form.reuseRelated || disabled}
            data-testid='event-analysis-always-fresh-namespaces'
          />
          <Input
            value={form.excludedNamespaces}
            label='Namespaces Excluded from Auto-Analysis'
            help='Comma-separated. Events in these namespaces are not analyzed automatically. You can still investigate them manually.'
            placeholder='e.g. sandbox'
            onChange={set('excludedNamespaces')}
            disabled={disabled}
            data-testid='event-analysis-excluded-namespaces'
          />
        </>
      )}
    </Box>
  );
};

export default EventAnalysisSettingsFields;
