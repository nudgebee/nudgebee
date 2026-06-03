import { Box, Typography } from '@mui/material';
import NDialog from '@common-new/modal/NDialog';
import CopyButton from '@common-new/CopyButton';
import { ds } from 'src/utils/colors';
import { buildKubectlCommand, getResourceDisplayName } from './utils';

const CliCommandModal = ({ rec, onClose }: { rec: any; onClose: () => void }) => {
  const command = buildKubectlCommand(rec);
  const resourceName = getResourceDisplayName(rec, '');
  const isPodRightSizing = rec.category === 'RightSizing' && rec.rule_name === 'pod_right_sizing';

  return (
    <NDialog
      open
      handleClose={onClose}
      width='sm'
      dialogTitle={isPodRightSizing ? 'kubectl Command' : 'Recommendation Details'}
      additionalComponent={null}
      isSubmitRequired={false}
      isCancelRequired={false}
      dialogContent={
        <>
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: ds.gray[600], mb: 'var(--ds-space-2)' }}>
            {isPodRightSizing
              ? `Run the following command to apply the recommended resource changes for ${resourceName}:`
              : `Details for recommendation on ${resourceName}:`}
          </Typography>
          <Box
            sx={{
              backgroundColor: 'var(--ds-brand-600)',
              borderRadius: 'var(--ds-radius-lg)',
              p: 'var(--ds-space-3) var(--ds-space-4)',
              fontFamily: 'monospace',
              fontSize: 'var(--ds-text-small)',
              color: 'var(--ds-brand-150)',
              lineHeight: 1.7,
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
              position: 'relative',
              maxHeight: ds.space.mul(0, 150),
              overflow: 'auto',
            }}
          >
            {command}
            <Box sx={{ position: 'absolute', top: ds.space[2], right: ds.space[2] }} data-testid='cli-copy-btn'>
              <CopyButton text={command} size='sm' />
            </Box>
          </Box>
        </>
      }
    />
  );
};

export default CliCommandModal;
