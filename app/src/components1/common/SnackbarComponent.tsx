import React, { type ReactNode, useEffect, useState } from 'react';
import Alert, { type AlertColor } from '@mui/material/Alert';
import { Snackbar } from '@mui/material';
import { snackbar } from './snackbarService';

export function SnackbarComponent() {
  const [open, setOpen] = useState(false);
  const [message, setMessage] = useState<ReactNode>('');
  const [severity, setSeverity] = useState<'success' | 'info' | 'warning' | 'error'>('success');

  useEffect(() => {
    const unsubscribe = snackbar.subscribe(({ message, severity }) => {
      setMessage(message);
      setSeverity(severity);
      setOpen(true);
    });

    return () => unsubscribe();
  }, []);

  const handleClose = () => setOpen(false);

  if (!open) {
    return null;
  }

  return (
    <Snackbar open={open} autoHideDuration={5000} onClose={handleClose} anchorOrigin={{ vertical: 'top', horizontal: 'right' }}>
      <Alert variant='filled' severity={severity as AlertColor} onClose={handleClose}>
        {message}
      </Alert>
    </Snackbar>
  );
}
