import { Popover, Box, Typography, List, ListItem, CircularProgress } from '@mui/material';
import { Button } from '@components1/ds/Button';
import PropTypes from 'prop-types';
import React, { useEffect, useRef, useCallback, useState } from 'react';
import FileDownloadIcon from '@mui/icons-material/FileDownload';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { toast as snackbar } from '@components1/ds/Toast';
import { ds } from '@utils/colors';

const ReferencesPopover = ({ anchorEl, open, onClose, references = [], accountId, conversationId }) => {
  const closeTimeoutRef = useRef(null);
  const isMouseInsidePopoverRef = useRef(false);

  // Clear any pending timeout
  const clearCloseTimeout = useCallback(() => {
    if (closeTimeoutRef.current) {
      clearTimeout(closeTimeoutRef.current);
      closeTimeoutRef.current = null;
    }
  }, []);

  // Start a timeout to close the popover
  const startCloseTimeout = useCallback(() => {
    clearCloseTimeout();
    closeTimeoutRef.current = setTimeout(() => {
      if (!isMouseInsidePopoverRef.current) {
        onClose();
      }
    }, 100); // Small delay to allow mouse to move into popover
  }, [onClose, clearCloseTimeout]);

  // Track mouse leaving the anchor element
  useEffect(() => {
    if (!anchorEl || !open) {
      return;
    }

    const handleAnchorMouseLeave = () => {
      startCloseTimeout();
    };

    anchorEl.addEventListener('mouseleave', handleAnchorMouseLeave);

    return () => {
      anchorEl.removeEventListener('mouseleave', handleAnchorMouseLeave);
      clearCloseTimeout();
    };
  }, [anchorEl, open, startCloseTimeout, clearCloseTimeout]);

  // Cleanup timeout on unmount
  useEffect(() => {
    return () => {
      clearCloseTimeout();
    };
  }, [clearCloseTimeout]);

  const handlePopoverMouseEnter = () => {
    isMouseInsidePopoverRef.current = true;
    clearCloseTimeout();
  };

  const handlePopoverMouseLeave = () => {
    isMouseInsidePopoverRef.current = false;
    onClose();
  };

  // Deduplicate references by URL
  const uniqueRefs = [];
  const seenUrls = new Set();

  references.forEach((ref) => {
    if (!seenUrls.has(ref.url)) {
      seenUrls.add(ref.url);
      uniqueRefs.push(ref);
    }
  });

  const [downloadingUrl, setDownloadingUrl] = useState(null);

  const handleDownloadFile = async (ref, e) => {
    e.preventDefault();
    if (!accountId || !conversationId) {
      snackbar.error('Missing account or conversation context to download file.');
      return;
    }

    try {
      setDownloadingUrl(ref.url);
      const data = {
        account_id: accountId,
        conversation_id: conversationId,
        path: ref.url,
        download: true,
      };
      const response = await apiAskNudgebee.getWorkspaceFile(data);

      if (response?.data !== undefined && response?.data !== null) {
        let fileContent = response.data;
        let fileUrl = '';
        let objectUrl = null;

        if (typeof fileContent === 'object') {
          // It's a JSON object payload
          const stringified = JSON.stringify(fileContent, null, 2);
          const blob = new Blob([stringified], { type: 'application/json' });
          fileUrl = window.URL.createObjectURL(blob);
          objectUrl = fileUrl;
        } else {
          // It's raw string text - always treat as content, not a URL
          const blob = new Blob([String(fileContent)], { type: 'text/plain' });
          fileUrl = window.URL.createObjectURL(blob);
          objectUrl = fileUrl;
        }

        const a = document.createElement('a');
        a.href = fileUrl;

        let downloadName = ref.text || ref.url || 'download';
        if (typeof fileContent === 'object' && !downloadName.endsWith('.json')) {
          downloadName += '.json';
        } else if (typeof fileContent === 'string' && objectUrl && !downloadName.includes('.')) {
          downloadName += '.txt';
        }

        a.download = downloadName;
        a.target = '_blank';
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);

        if (objectUrl) {
          setTimeout(() => window.URL.revokeObjectURL(objectUrl), 100);
        }
      } else {
        snackbar.error('Failed to download the file.');
      }
    } catch (error) {
      console.error('Download error:', error);
      snackbar.error('An error occurred while downloading the file.');
    } finally {
      setDownloadingUrl(null);
    }
  };

  return (
    <Popover
      open={open}
      anchorEl={anchorEl}
      onClose={onClose}
      disableScrollLock={true}
      disableAutoFocus={true}
      anchorOrigin={{
        vertical: 'top',
        horizontal: 'right',
      }}
      transformOrigin={{
        vertical: 'top',
        horizontal: 'left',
      }}
      sx={{
        pointerEvents: 'none',
        zIndex: 1500,
        '& .MuiPopover-paper': {
          pointerEvents: 'auto',
          mt: ds.space[2],
          boxShadow: '0px 4px 20px rgba(0, 0, 0, 0.15)',
          borderRadius: ds.radius.lg,
          border: '1px solid var(--ds-gray-200)',
          maxWidth: ds.space.mul(1, 100),
          minWidth: ds.space.mul(1, 70),
        },
      }}
      slotProps={{
        paper: {
          onMouseEnter: handlePopoverMouseEnter,
          onMouseLeave: handlePopoverMouseLeave,
        },
      }}
    >
      <Box sx={{ p: ds.space[4] }}>
        <Typography
          sx={{
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            color: 'var(--ds-blue-500)',
            mb: ds.space[3],
            fontFamily: '"Poppins", sans-serif',
            pb: ds.space[2],
            borderBottom: '1px solid var(--ds-gray-200)',
          }}
        >
          References ({uniqueRefs.length})
        </Typography>
        <List sx={{ p: 0, maxHeight: ds.space.mul(1, 75), overflowY: 'auto' }}>
          {uniqueRefs.map((ref) => (
            <ListItem
              key={ref.url}
              sx={{
                p: 0,
                mb: ds.space[2],
                display: 'block',
                '&:last-child': {
                  mb: 0,
                },
              }}
            >
              {ref.type === 'file' ? (
                <Box
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    width: '100%',
                  }}
                >
                  <Typography
                    sx={{
                      color: 'var(--ds-gray-700)',
                      fontSize: 'var(--ds-text-body)',
                      lineHeight: '1.5',
                      wordBreak: 'break-word',
                      fontFamily: '"Poppins", sans-serif',
                    }}
                  >
                    • {ref.text || ref.url}
                  </Typography>
                  <Button
                    tone='ghost'
                    size='sm'
                    icon={downloadingUrl === ref.url ? <CircularProgress size={16} /> : <FileDownloadIcon fontSize='small' />}
                    onClick={(e) => handleDownloadFile(ref, e)}
                    disabled={downloadingUrl === ref.url}
                    aria-label='Download file'
                  />
                </Box>
              ) : (
                <a
                  href={ref.url}
                  target='_blank'
                  rel='noopener noreferrer'
                  style={{
                    color: 'var(--ds-gray-700)',
                    textDecoration: 'none',
                    fontSize: 'var(--ds-text-body)',
                    lineHeight: '1.5',
                    display: 'block',
                    wordBreak: 'break-word',
                    fontFamily: '"Poppins", sans-serif',
                  }}
                  onMouseOver={(e) => (e.target.style.textDecoration = 'underline')}
                  onMouseOut={(e) => (e.target.style.textDecoration = 'none')}
                  onFocus={(e) => (e.target.style.textDecoration = 'underline')}
                  onBlur={(e) => (e.target.style.textDecoration = 'none')}
                >
                  • {ref.text || ref.url}
                </a>
              )}
            </ListItem>
          ))}
        </List>
      </Box>
    </Popover>
  );
};

ReferencesPopover.propTypes = {
  anchorEl: PropTypes.any,
  open: PropTypes.bool.isRequired,
  onClose: PropTypes.func.isRequired,
  references: PropTypes.array,
  accountId: PropTypes.string,
  conversationId: PropTypes.string,
};

export default ReferencesPopover;
