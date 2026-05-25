import { useState } from 'react';
import { Box, Typography, Dialog, DialogTitle, DialogContent, IconButton } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import CheckIcon from '@mui/icons-material/Check';
import ContentCopyOutlinedIcon from '@mui/icons-material/ContentCopyOutlined';
import { colors } from 'src/utils/colors';
import { buildKubectlCommand, getResourceDisplayName } from './utils';

const CliCommandModal = ({ rec, onClose }: { rec: any; onClose: () => void }) => {
  const [copied, setCopied] = useState(false);
  const command = buildKubectlCommand(rec);
  const resourceName = getResourceDisplayName(rec, '');
  const isPodRightSizing = rec.category === 'RightSizing' && rec.rule_name === 'pod_right_sizing';

  const handleCopy = () => {
    navigator.clipboard.writeText(command);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <Dialog open onClose={onClose} maxWidth='sm' fullWidth data-testid='cli-command-modal' PaperProps={{ sx: { borderRadius: '12px' } }}>
      <DialogTitle
        sx={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          p: '16px 20px',
          borderBottom: `1px solid ${colors.border.secondaryLightest}`,
        }}
      >
        <Typography sx={{ fontSize: '16px', fontWeight: 600, color: colors.text.secondary }}>
          {isPodRightSizing ? 'kubectl Command' : 'Recommendation Details'}
        </Typography>
        <IconButton onClick={onClose} size='small' sx={{ color: colors.text.tertiary }}>
          <CloseIcon sx={{ fontSize: '20px' }} />
        </IconButton>
      </DialogTitle>
      <DialogContent sx={{ p: '20px' }}>
        <Typography sx={{ fontSize: '12px', color: colors.text.tertiary, mb: '8px' }}>
          {isPodRightSizing
            ? `Run the following command to apply the recommended resource changes for ${resourceName}:`
            : `Details for recommendation on ${resourceName}:`}
        </Typography>
        <Box
          sx={{
            backgroundColor: '#1E293B',
            borderRadius: '8px',
            p: '14px 16px',
            fontFamily: 'monospace',
            fontSize: '12px',
            color: '#E2E8F0',
            lineHeight: 1.7,
            whiteSpace: 'pre-wrap',
            wordBreak: 'break-all',
            position: 'relative',
            maxHeight: '300px',
            overflow: 'auto',
          }}
        >
          {command}
          <IconButton
            onClick={handleCopy}
            size='small'
            data-testid='cli-copy-btn'
            sx={{
              position: 'absolute',
              top: '8px',
              right: '8px',
              color: copied ? '#4ADE80' : '#94A3B8',
              '&:hover': { color: '#E2E8F0', backgroundColor: 'rgba(255,255,255,0.1)' },
            }}
          >
            {copied ? <CheckIcon sx={{ fontSize: '16px' }} /> : <ContentCopyOutlinedIcon sx={{ fontSize: '16px' }} />}
          </IconButton>
        </Box>
        {copied && <Typography sx={{ fontSize: '11px', color: '#4ADE80', mt: '6px' }}>Copied to clipboard</Typography>}
      </DialogContent>
    </Dialog>
  );
};

export default CliCommandModal;
