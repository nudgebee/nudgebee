import React, { useState } from 'react';
import { Box, Typography, TextField, IconButton } from '@mui/material';
import EditIcon from '@mui/icons-material/Edit';
import CheckIcon from '@mui/icons-material/Check';
import CloseIcon from '@mui/icons-material/Close';
import NewToggleButtons from './NewToggleButtons';
import CustomButton from '@common/NewCustomButton';
import { colors } from 'src/utils/colors';
import CustomBackArrow from '@components1/common/CustomBackButton';
import { useRouter } from 'next/router';
interface WorkflowHeaderProps {
  workflowTitle?: string;
  onTabChange?: (tab: string) => void;
  activeTab?: string;
  onTitleChange?: (title: string) => void;
  allowTitleEdit?: boolean;
  accountId?: string;
  onOpenAiChat?: () => void;
  showAiChatButton?: boolean;
}

const WorkflowHeader: React.FC<WorkflowHeaderProps> = ({
  workflowTitle = 'Automation Title',
  onTabChange,
  activeTab = 'editor',
  onTitleChange,
  allowTitleEdit = false,
  accountId,
  onOpenAiChat,
  showAiChatButton = false,
}) => {
  const [isEditingTitle, setIsEditingTitle] = useState(false);
  const [editedTitle, setEditedTitle] = useState(workflowTitle);

  const handleTitleEdit = () => {
    setEditedTitle(workflowTitle);
    setIsEditingTitle(true);
  };

  const handleTitleSave = () => {
    if (onTitleChange && editedTitle.trim()) {
      onTitleChange(editedTitle.trim());
    }
    setIsEditingTitle(false);
  };

  const handleTitleCancel = () => {
    setEditedTitle(workflowTitle);
    setIsEditingTitle(false);
  };

  const handleTitleKeyPress = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      handleTitleSave();
    } else if (e.key === 'Escape') {
      handleTitleCancel();
    }
  };

  const tabOptions = [
    { value: 'editor', label: 'Editor' },
    { value: 'executions', label: 'Executions' },
  ];

  const router = useRouter();

  const handleBack = () => {
    // Check for returnUrl query param first (for navigation from investigate pages)
    const { returnUrl } = router.query;
    if (returnUrl) {
      router.push(decodeURIComponent(returnUrl as string));
    } else {
      const backButtonPath = `/auto-pilot?accountId=${accountId}#workflow`;
      router.push(backButtonPath);
    }
  };

  return (
    <Box
      sx={{
        top: 0,
        left: 0,
        right: 0,
        height: '60px',
        backgroundColor: '#F9F9F9',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        padding: '0 20px',
        zIndex: 10,
        borderBottom: '1px solid rgb(229, 229, 229)',
      }}
    >
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
        <CustomBackArrow id='workflow-back-btn' useNewIcon onClick={handleBack} />

        {isEditingTitle ? (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
            <TextField
              value={editedTitle}
              onChange={(e) => setEditedTitle(e.target.value)}
              onKeyDown={handleTitleKeyPress}
              size='small'
              sx={{
                '& .MuiOutlinedInput-root': {
                  fontSize: '16px',
                  fontWeight: 500,
                  color: colors.text.secondary,
                },
                '& .MuiOutlinedInput-input': {
                  padding: '4px 8px',
                },
              }}
            />
            <IconButton id='workflow-title-save-btn' size='small' onClick={handleTitleSave} sx={{ color: '#4caf50', padding: '4px' }}>
              <CheckIcon fontSize='small' />
            </IconButton>
            <IconButton id='workflow-title-cancel-btn' size='small' onClick={handleTitleCancel} sx={{ color: '#f44336', padding: '4px' }}>
              <CloseIcon fontSize='small' />
            </IconButton>
          </Box>
        ) : (
          <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
            <Typography
              variant='h6'
              sx={{
                color: colors.text.secondary,
                fontSize: '16px',
                fontWeight: 500,
                margin: 0,
              }}
            >
              {workflowTitle}
            </Typography>

            {allowTitleEdit && onTitleChange && (
              <IconButton id='workflow-title-edit-btn' size='small' onClick={handleTitleEdit} sx={{ color: '#6c757d', padding: '4px' }}>
                <EditIcon fontSize='small' />
              </IconButton>
            )}
          </Box>
        )}
      </Box>

      {/* Tab Toggle - Centered on bottom border - Only show if onTabChange is provided */}
      {onTabChange && (
        <Box
          sx={{
            position: 'absolute',
            left: '50%',
            top: '24px',
            transform: 'translateX(-50%)',
            zIndex: 11,
          }}
        >
          <NewToggleButtons options={tabOptions} noShadow={true} activeValue={activeTab} onChange={(value) => onTabChange?.(value)} width='260px' />
        </Box>
      )}

      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
        {/* AI Chat Button - Only show if conversation exists */}
        {showAiChatButton && onOpenAiChat && (
          <CustomButton
            id='workflow-continue-ai-btn'
            onClick={onOpenAiChat}
            text='Continue with AI'
            variant='secondary'
            size='Medium'
            sx={{
              backgroundColor: '#f0f9ff',
              color: '#0369a1',
              '&:hover': {
                backgroundColor: '#e0f2fe',
              },
            }}
          />
        )}
      </Box>
    </Box>
  );
};

export default WorkflowHeader;
