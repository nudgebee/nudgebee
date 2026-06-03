import { Box, Typography } from '@mui/material';
import { Button } from '@components1/ds/Button';

interface TriggerWarningMessageProps {
  onAddTrigger: () => void;
}

const TriggerWarningMessage: React.FC<TriggerWarningMessageProps> = ({ onAddTrigger }) => {
  return (
    <Box
      sx={{
        position: 'absolute',
        top: '100px',
        left: '50%',
        transform: 'translateX(-50%)',
        backgroundColor: 'rgba(255, 193, 7, 0.1)',
        border: '1px solid var(--ds-amber-400)',
        borderRadius: 'var(--ds-radius-xl)',
        padding: 'var(--ds-space-3) var(--ds-space-4)',
        display: 'flex',
        alignItems: 'center',
        gap: 'var(--ds-space-3)',
        zIndex: 10,
        boxShadow: '0 2px 8px rgba(0, 0, 0, 0.1)',
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
