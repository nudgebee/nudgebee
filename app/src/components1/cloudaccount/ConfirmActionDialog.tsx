import React from 'react';
import { Typography, Box } from '@mui/material';
import { Input } from '@components1/ds/Input';
import NDialog from '@components1/common/modal/NDialog';
import { ds } from '@utils/colors';
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
          <Typography sx={{ mb: ds.space[4] }}>
            You are about to <strong>{action.label.toLowerCase()}</strong> resource <strong>{resourceName}</strong>. This is a destructive action.
          </Typography>
          <Typography sx={{ mb: ds.space[2] }}>
            Type <strong>{resourceName}</strong> to confirm:
          </Typography>
          <Box data-testid='strict-confirm-input'>
            <Input size='sm' value={confirmInput} onChange={onConfirmInputChange} placeholder={resourceName} />
          </Box>
        </>
      ) : (
        <Typography>{action.confirmationMessage}</Typography>
      )}

      {action.requiresArgs &&
        action.argsConfig?.map((arg) => (
          <Box key={arg.field} sx={{ mt: ds.space[4] }} data-testid={`action-arg-${arg.field}`}>
            <Input
              label={arg.label}
              type={arg.type as 'text' | 'number'}
              size='sm'
              value={String(actionArgs[arg.field] ?? '')}
              onChange={(value) =>
                onActionArgsChange({
                  ...actionArgs,
                  // Empty string stays empty (don't coerce to 0) so users can clear a numeric field
                  [arg.field]: arg.type === 'number' ? (value === '' ? '' : Number(value)) : value,
                })
              }
            />
          </Box>
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
