import { useState, useRef, useEffect } from 'react';
import CheckIcon from '@mui/icons-material/Check';
import { Button } from '@components1/ds/Button';
import { toast } from '@components1/ds/Toast';
import SafeIcon from '@components1/common/SafeIcon';
import { CopyIcon } from '@assets';

const ICON_SIZE = { xs: 12, sm: 14, md: 16, lg: 18 };

/**
 * CopyButton — convenience wrapper around the DS Button for copy-to-clipboard.
 *
 * Props:
 *   text         — string to copy to clipboard (required)
 *   size         — Button size token: 'xs' | 'sm' | 'md' | 'lg' (default: 'md')
 *   toastMessage — if provided, shows a success toast with this message after copying
 */
const CopyButton = ({ text, size = 'md', tone = 'ghost', toastMessage }) => {
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

  return (
    <Button
      tone={tone}
      composition='icon-only'
      size={size}
      icon={copied ? <SafeIcon src={CheckIcon} alt='Copied' width={px} height={px} /> : <SafeIcon src={CopyIcon} alt='Copy' width={px} height={px} />}
      aria-label={copied ? 'Copied' : 'Copy'}
      onClick={handleClick}
    />
  );
};

export default CopyButton;
