import React from 'react';
import { TextField, Typography, Box } from '@mui/material';
import NDialog from '@components1/common/modal/NDialog';
import type { ResourceAction } from './resourceActions';

interface ConfirmActionDialogProps {
  open: boolean;
  action: ResourceAction | null;
  resource: any | null;
  loading: boolean;
  confirmInput: string;
  isStrictConfirmValid: boolean;
  actionArgs: Record<string, any>;
  onConfirmInputChange: (value: string) => void;
  onActionArgsChange: (args: Record<string, any>) => void;
  onConfirm: () => void;
  onCancel: () => void;
}

const ConfirmActionDialog: React.FC<ConfirmActionDialogProps> = ({
  open,
  action,
  resource,
  loading,
  confirmInput,
  isStrictConfirmValid,
  actionArgs,
  onConfirmInputChange,
  onActionArgsChange,
  onConfirm,
  onCancel,
}) => {
  if (!action || !resource) return null;

  const resourceName = resource.name || resource.resourse_id;
  const isStrict = action.confirmationType === 'strict';

  const dialogContent = (
    <Box>
      {isStrict ? (
        <>
          <Typography sx={{ mb: 2 }}>
            You are about to <strong>{action.label.toLowerCase()}</strong> resource <strong>{resourceName}</strong>. This is a destructive action.
          </Typography>
          <Typography sx={{ mb: 1 }}>
            Type <strong>{resourceName}</strong> to confirm:
          </Typography>
          <TextField
            fullWidth
            size='small'
            autoFocus
            value={confirmInput}
            onChange={(e) => onConfirmInputChange(e.target.value)}
            placeholder={resourceName}
            data-testid='strict-confirm-input'
          />
        </>
      ) : (
        <Typography>{action.confirmationMessage}</Typography>
      )}

      {action.requiresArgs &&
        action.argsConfig?.map((arg) => (
          <TextField
            key={arg.field}
            label={arg.label}
            type={arg.type}
            fullWidth
            size='small'
            sx={{ mt: 2 }}
            value={actionArgs[arg.field] || ''}
            onChange={(e) =>
              onActionArgsChange({
                ...actionArgs,
                [arg.field]: arg.type === 'number' ? Number(e.target.value) : e.target.value,
              })
            }
            data-testid={`action-arg-${arg.field}`}
          />
        ))}
    </Box>
  );

  return (
    <NDialog
      open={open}
      dialogTitle={action.label}
      dialogContent={null}
      additionalComponent={dialogContent}
      buttonText={action.destructive ? action.label : 'Confirm'}
      handleSubmit={onConfirm}
      handleClose={onCancel}
      disabled={isStrict && !isStrictConfirmValid}
      loading={loading}
      width='sm'
    />
  );
};

export default ConfirmActionDialog;
