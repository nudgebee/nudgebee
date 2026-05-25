import { Box, Typography, Button } from '@mui/material';

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
        border: '1px solid #ffc107',
        borderRadius: '12px',
        padding: '12px 20px',
        display: 'flex',
        alignItems: 'center',
        gap: '12px',
        zIndex: 10,
        boxShadow: '0 2px 8px rgba(0, 0, 0, 0.1)',
      }}
    >
      <Typography variant='body2' sx={{ fontWeight: 500, color: '#e65100' }}>
        ⚠️ Consider adding a trigger node to define how this workflow starts
      </Typography>
      <Button
        size='small'
        variant='outlined'
        onClick={onAddTrigger}
        sx={{
          fontSize: '12px',
          padding: '4px 12px',
          borderColor: '#ffc107',
          color: '#e65100',
          '&:hover': {
            borderColor: '#e65100',
            backgroundColor: 'rgba(255, 193, 7, 0.1)',
          },
        }}
      >
        Add Trigger
      </Button>
    </Box>
  );
};

export default TriggerWarningMessage;
