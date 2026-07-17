import { useState, useRef, useEffect } from 'react';
import { Box } from '@mui/material';
import CheckIcon from '@mui/icons-material/Check';
import { Button } from '@ui/Button';
import { toast } from '@ui/Toast';
import SafeIcon from '@shared/icons/SafeIcon';
import { CopyIcon } from '@assets';
import { ds } from '@utils/colors';

const ICON_SIZE = { xs: 12, sm: 14, md: 16, lg: 18 };

// Smooth cross-fade timing: copy shrinks out while the check grows in.
const SWAP_TRANSITION = 'transform 0.35s cubic-bezier(0.34, 1.56, 0.64, 1), opacity 0.3s ease';

/**
 * CopyButton — convenience wrapper around the DS Button for copy-to-clipboard.
 *
 * Props:
 *   text         — string to copy to clipboard (required)
 *   size         — Button size token: 'xs' | 'sm' | 'md' | 'lg' (default: 'md')
 *   toastMessage — if provided, shows a success toast with this message after copying
 */
const CopyButton = ({ text, size = 'md', tone = 'ghost', toastMessage = undefined }) => {
  const [copied, setCopied] = useState(false);
  const timerRef = useRef(null);

  useEffect(() => {
    return () => clearTimeout(timerRef.current);
  }, []);

  const handleClick = (e) => {
    e.stopPropagation();
    navigator.clipboard
      .writeText(text ?? '')
      .then(() => {
        setCopied(true);
        timerRef.current = setTimeout(() => setCopied(false), 2000);
        if (toastMessage) {
          toast.success(toastMessage);
        }
      })
      .catch(() => {
        toast.error('Failed to copy to clipboard');
      });
  };

  const px = ICON_SIZE[size] ?? 16;
  // Check reads thin/light at the copy-icon size, so render it a touch larger and in strong success-green.
  const checkPx = px + 4;

  // Both icons are stacked; on copy the copy icon scales down + fades out while the check scales up + fades in.
  const icon = (
    <Box
      component='span'
      sx={{ position: 'relative', display: 'inline-flex', width: checkPx, height: checkPx, alignItems: 'center', justifyContent: 'center' }}
    >
      <Box
        component='span'
        sx={{
          position: 'absolute',
          display: 'inline-flex',
          transition: SWAP_TRANSITION,
          transform: copied ? 'scale(0.4)' : 'scale(1)',
          opacity: copied ? 0 : 1,
        }}
      >
        <SafeIcon src={CopyIcon} alt='Copy' width={px} height={px} />
      </Box>
      <Box
        component='span'
        sx={{
          position: 'absolute',
          display: 'inline-flex',
          color: ds.green[500],
          transition: SWAP_TRANSITION,
          transform: copied ? 'scale(1)' : 'scale(0.4)',
          opacity: copied ? 1 : 0,
        }}
      >
        <SafeIcon src={CheckIcon} alt='Copied' width={checkPx} height={checkPx} />
      </Box>
    </Box>
  );

  return <Button tone={tone} composition='icon-only' size={size} icon={icon} aria-label={copied ? 'Copied' : 'Copy'} onClick={handleClick} />;
};

export default CopyButton;
