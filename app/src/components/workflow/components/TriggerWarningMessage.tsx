import { Box, Typography } from '@mui/material';
import { Button } from '@ui/Button';

interface TriggerWarningMessageProps {
  onAddTrigger: () => void;
}

const TriggerWarningMessage: React.FC<TriggerWarningMessageProps> = ({ onAddTrigger }) => {
  return (
    <Box
      sx={{
        position: 'absolute',
        top: 'calc(var(--ds-space-0) * 50)',
        left: '50%',
        transform: 'translateX(-50%)',
        backgroundColor: 'color-mix(in srgb, var(--ds-amber-500) 10%, transparent)',
        border: '1px solid var(--ds-amber-400)',
        borderRadius: 'var(--ds-radius-xl)',
        padding: 'var(--ds-space-3) var(--ds-space-4)',
        display: 'flex',
        alignItems: 'center',
        gap: 'var(--ds-space-3)',
        zIndex: 10,
        boxShadow: '0 var(--ds-space-0) var(--ds-space-2) var(--ds-gray-alpha-300)',
      }}
    >
      <Typography variant='body2' sx={{ fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-amber-500)' }}>
        ⚠️ Consider adding a trigger node to define how this workflow starts
      </Typography>
      <Button tone='secondary' size='sm' onClick={onAddTrigger}>
        Add Trigger
      </Button>
    </Box>
  );
};

export default TriggerWarningMessage;
