import React, { useEffect, useRef, useState } from 'react';
import { Box, Typography, CircularProgress } from '@mui/material';
import ArrowForwardRoundedIcon from '@mui/icons-material/ArrowForwardRounded';
import ErrorOutlineRoundedIcon from '@mui/icons-material/ErrorOutlineRounded';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import ScheduleRoundedIcon from '@mui/icons-material/ScheduleRounded';
import ImageOutlinedIcon from '@mui/icons-material/ImageOutlined';
import SafeIcon from '@shared/icons/SafeIcon';
import { DeleteIconRed, EditIcon } from '@assets';
import Tooltip from '@ui/Tooltip';
import { Button } from '@ui/Button';
import { Input } from '@ui/Input';
import { ds } from '@utils/colors';
import { QueuedMessage } from '@hooks/useQueuedMessages';

export interface QueuedMessagesBannerProps {
  queuedMessages: QueuedMessage[];
  onRefresh?: () => void;
  onRestore?: (id: string) => void;
  isExpanded: boolean;
  maxQueuedMessages?: number;
  /** The conversation ended without completing, so nothing will run waiting rows except Send now. */
  isPaused?: boolean;
  onToggleExpanded: () => void;
  onSendNow: (id: string) => Promise<void> | void;
  /** Saves new text for a queued message in place; rejects with a user-facing error. */
  onUpdate: (id: string, text: string) => Promise<void>;
  onDelete: (id: string) => Promise<void> | void;
}

const ACTIONS_CLASS = 'queued-message-actions';

// A tray docked on top of the composer: it reads as part of the input, not as
// another card in the transcript.
export const QueuedMessagesBanner: React.FC<QueuedMessagesBannerProps> = ({
  queuedMessages,
  onRefresh,
  onRestore,
  isExpanded,
  maxQueuedMessages,
  isPaused = false,
  onToggleExpanded,
  onSendNow,
  onUpdate,
  onDelete,
}) => {
  const [deletingMessageId, setDeletingMessageId] = useState<string>();
  const [sendingMessageId, setSendingMessageId] = useState<string>();
  // In-place edit: the message keeps its queue position and attachments.
  const [editingId, setEditingId] = useState<string>();
  const [draft, setDraft] = useState('');
  const [savingEdit, setSavingEdit] = useState(false);
  const [editError, setEditError] = useState<string>();
  const editorRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (editingId) editorRef.current?.querySelector<HTMLElement>('textarea, input')?.focus();
  }, [editingId]);
  const isDeleting = queuedMessages?.some((message) => message.id === deletingMessageId);
  const isSending = queuedMessages?.some((message) => message.id === sendingMessageId);
  const isEditingAny = queuedMessages?.some((message) => message.id === editingId);

  const cancelEdit = () => {
    setEditingId(undefined);
    setEditError(undefined);
  };
  const saveEdit = async (id: string) => {
    if (!draft.trim() || savingEdit) return;
    if (draft.trim() === queuedMessages.find((m) => m.id === id)?.text) {
      cancelEdit();
      return;
    }
    setSavingEdit(true);
    setEditError(undefined);
    try {
      await onUpdate(id, draft);
      setEditingId(undefined);
    } catch (error) {
      setEditError(error instanceof Error ? error.message : 'Couldn’t save the edit.');
    } finally {
      setSavingEdit(false);
    }
  };

  if (!queuedMessages || queuedMessages.length === 0) {
    return null;
  }

  const count = queuedMessages.filter((message) => !message.saveError && !message.isForeground).length;
  const waiting = queuedMessages.filter((message) => !message.saving && !message.saveError).length;
  const unconfirmed = queuedMessages.filter((message) => message.saveError).length;
  const isFull = maxQueuedMessages !== undefined && count >= maxQueuedMessages;
  const hint = isFull
    ? 'Queue full (wait for tasks to finish or delete a message)'
    : unconfirmed
    ? `${unconfirmed} need${unconfirmed === 1 ? 's' : ''} attention`
    : waiting && isPaused
    ? 'Paused: nothing is running. Send now or remove.'
    : waiting
    ? 'Runs after the current response'
    : 'Saving…';

  return (
    <Box
      data-testid='queued-messages-banner'
      sx={{
        mx: ds.space[3],
        backgroundColor: ds.background[200],
        border: `1px solid ${ds.gray[200]}`,
        borderBottom: 0,
        borderRadius: `${ds.radius.xl} ${ds.radius.xl} 0 0`,
        overflow: 'hidden',
        boxSizing: 'border-box',
      }}
    >
      <Box
        role='button'
        tabIndex={0}
        aria-expanded={isExpanded}
        onClick={onToggleExpanded}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            onToggleExpanded();
          }
        }}
        data-testid='queued-messages-header'
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: ds.space[2],
          px: ds.space[3],
          py: ds.space[1],
          minHeight: ds.space[6],
          cursor: 'pointer',
          userSelect: 'none',
          '&:hover': { backgroundColor: ds.gray.alpha[100] },
          '&:focus-visible': { outline: `2px solid ${ds.blue[500]}`, outlineOffset: '-2px' },
        }}
      >
        <ScheduleRoundedIcon sx={{ fontSize: 14, color: ds.gray[500] }} />
        <Typography
          component='span'
          sx={{ fontFamily: ds.font.display, fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.gray[700], lineHeight: 1 }}
        >
          Queued
        </Typography>
        <Box
          component='span'
          data-testid='queued-messages-count'
          sx={{
            px: ds.space[1],
            borderRadius: ds.radius.pill,
            backgroundColor: isFull ? ds.amber[100] : ds.gray.alpha[200],
            color: isFull ? ds.amber[700] : ds.gray[600],
            fontSize: ds.text.caption,
            fontWeight: ds.weight.medium,
            fontVariantNumeric: 'tabular-nums',
            lineHeight: 1.6,
          }}
        >
          {maxQueuedMessages ? `${count}/${maxQueuedMessages}` : count}
        </Box>
        <Typography
          component='span'
          sx={{
            flex: 1,
            minWidth: 0,
            fontSize: ds.text.caption,
            color: isFull ? ds.amber[700] : unconfirmed ? ds.red[600] : ds.gray[500],
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          }}
        >
          {hint}
        </Typography>
        <KeyboardArrowDownIcon
          sx={{
            fontSize: 18,
            color: ds.gray[500],
            transform: isExpanded ? 'rotate(180deg)' : 'none',
            transition: `transform ${ds.motion.micro} ${ds.motion.ease}`,
          }}
        />
      </Box>

      {isExpanded && (
        <Box component='ol' role='list' sx={{ m: 0, p: 0, listStyle: 'none', maxHeight: '40vh', overflowY: 'auto', overflowX: 'hidden' }}>
          {queuedMessages.map((msg, index) => {
            // Run order counts only confirmed rows; pending and failed rows get a status icon instead.
            const position = queuedMessages.slice(0, index + 1).filter((m) => !m.saving && !m.saveError).length;
            const hasImages = Boolean(msg.images && msg.images.length > 0);
            const isEditing = editingId === msg.id;
            const isRowDeleting = deletingMessageId === msg.id;
            const isRowSending = sendingMessageId === msg.id;
            const busy = isEditingAny || isDeleting || isSending || msg.saving || Boolean(msg.saveError);
            const showActions = !msg.saving && !msg.saveError && !isEditing;
            return (
              <Box
                component='li'
                key={msg.id}
                data-testid={`queued-message-item-${msg.id}`}
                sx={{
                  display: 'flex',
                  flexWrap: 'wrap',
                  alignItems: 'center',
                  gap: ds.space[2],
                  px: ds.space[3],
                  py: ds.space[1],
                  minHeight: ds.space[6],
                  borderTop: `1px solid ${ds.gray[200]}`,
                  opacity: msg.saving ? 0.7 : 1,
                  [`& .${ACTIONS_CLASS}`]: {
                    opacity: isRowDeleting || isRowSending ? 1 : 0,
                    transition: `opacity ${ds.motion.micro} ${ds.motion.ease}`,
                  },
                  [`&:hover .${ACTIONS_CLASS}, &:focus-within .${ACTIONS_CLASS}`]: { opacity: 1 },
                  '@media (hover: none)': { [`& .${ACTIONS_CLASS}`]: { opacity: 1 } },
                  '&:hover': { backgroundColor: ds.gray.alpha[100] },
                }}
              >
                <Box
                  component='span'
                  aria-hidden
                  sx={{
                    flexShrink: 0,
                    width: ds.space[4],
                    textAlign: 'center',
                    fontSize: ds.text.caption,
                    fontWeight: ds.weight.medium,
                    color: ds.gray[500],
                    fontVariantNumeric: 'tabular-nums',
                  }}
                >
                  {msg.saving ? (
                    <CircularProgress size={10} thickness={6} sx={{ color: ds.gray[500] }} />
                  ) : msg.saveError ? (
                    <ErrorOutlineRoundedIcon sx={{ fontSize: 14, color: ds.red[600], verticalAlign: 'middle' }} />
                  ) : (
                    position
                  )}
                </Box>

                {isEditing ? (
                  <Box
                    ref={editorRef}
                    sx={{
                      '& textarea': { fontFamily: 'inherit' },
                      flex: 1,
                      minWidth: 0,
                      display: 'flex',
                      flexDirection: 'column',
                      gap: ds.space[1],
                      py: ds.space[1],
                    }}
                  >
                    <Input
                      type='textarea'
                      size='sm'
                      minRows={1}
                      maxRows={4}
                      value={draft}
                      onChange={setDraft}
                      disabled={savingEdit}
                      error={editError}
                      data-testid={`queued-message-editor-${msg.id}`}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter' && !e.shiftKey) {
                          e.preventDefault();
                          void saveEdit(msg.id);
                        } else if (e.key === 'Escape') {
                          e.preventDefault();
                          e.stopPropagation();
                          if (!savingEdit) cancelEdit();
                        }
                      }}
                    />
                    <Box sx={{ display: 'flex', gap: ds.space[1], justifyContent: 'flex-end' }}>
                      <Button tone='ghost' size='xs' onClick={cancelEdit} disabled={savingEdit} data-testid={`queued-message-edit-cancel-${msg.id}`}>
                        Cancel
                      </Button>
                      <Button
                        tone='primary'
                        size='xs'
                        onClick={() => void saveEdit(msg.id)}
                        loading={savingEdit}
                        disabled={!draft.trim()}
                        data-testid={`queued-message-edit-save-${msg.id}`}
                      >
                        Save
                      </Button>
                    </Box>
                  </Box>
                ) : (
                  <Tooltip title={msg.text} placement='top-start' enterDelay={600} enterNextDelay={600}>
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], flex: 1, minWidth: 0 }}>
                      {msg.user && msg.user !== 'You' && (
                        <Typography
                          component='span'
                          sx={{
                            flexShrink: 0,
                            px: ds.space[1],
                            borderRadius: ds.radius.sm,
                            backgroundColor: ds.gray.alpha[200],
                            color: ds.gray[700],
                            fontSize: ds.text.caption,
                            fontWeight: ds.weight.medium,
                            lineHeight: 1.6,
                          }}
                        >
                          {msg.user}
                        </Typography>
                      )}
                      {hasImages && (
                        <ImageOutlinedIcon titleAccess='Has image attachment' sx={{ fontSize: 14, color: ds.gray[500], flexShrink: 0 }} />
                      )}
                      <Typography
                        sx={{
                          fontSize: ds.text.body,
                          color: ds.gray[700],
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                          lineHeight: 1.5,
                        }}
                      >
                        {msg.text || (hasImages ? 'Attached image' : `Message ${index + 1}`)}
                      </Typography>
                    </Box>
                  </Tooltip>
                )}

                {isRowDeleting && (
                  <Typography role='status' sx={{ flexShrink: 0, fontSize: ds.text.caption, color: ds.gray[500] }}>
                    Deleting…
                  </Typography>
                )}
                {msg.saving && (
                  <Typography role='status' sx={{ flexShrink: 0, fontSize: ds.text.caption, color: ds.gray[500] }}>
                    Saving…
                  </Typography>
                )}
                {msg.saveError && (
                  <Box
                    sx={{
                      display: 'flex',
                      flexWrap: 'wrap',
                      alignItems: 'center',
                      columnGap: ds.space[2],
                      flexBasis: '100%',
                      minWidth: 0,
                      pl: ds.space[5],
                    }}
                  >
                    <Typography role='alert' sx={{ fontSize: ds.text.caption, color: ds.red[600] }}>
                      {msg.saveError}
                    </Typography>
                    {msg.notAdmitted ? (
                      <Button tone='link' size='xs' onClick={() => onRestore?.(msg.id)}>
                        Move to composer
                      </Button>
                    ) : onRefresh ? (
                      <Button tone='link' size='xs' onClick={onRefresh}>
                        Refresh history
                      </Button>
                    ) : null}
                  </Box>
                )}

                {showActions && (
                  <Box className={ACTIONS_CLASS} sx={{ display: 'flex', alignItems: 'center', gap: ds.space[0], flexShrink: 0 }}>
                    <Button
                      tone='ghost'
                      size='xs'
                      composition='icon-only'
                      icon={<SafeIcon src={EditIcon} alt='' width={14} height={14} />}
                      tooltip='Edit'
                      aria-label='Edit message'
                      data-testid={`queued-message-edit-${msg.id}`}
                      disabled={busy}
                      onClick={(e) => {
                        e.stopPropagation();
                        setDraft(msg.text);
                        setEditError(undefined);
                        setEditingId(msg.id);
                      }}
                    />
                    <Button
                      tone='ghost'
                      size='xs'
                      composition='icon-only'
                      icon={<SafeIcon src={DeleteIconRed} alt='' width={14} height={14} />}
                      tooltip='Remove from queue'
                      aria-label='Delete message'
                      data-testid={`queued-message-delete-${msg.id}`}
                      disabled={busy}
                      loading={isRowDeleting}
                      onClick={async (e) => {
                        e.stopPropagation();
                        setDeletingMessageId(msg.id);
                        try {
                          await onDelete(msg.id);
                        } finally {
                          setDeletingMessageId(undefined);
                        }
                      }}
                    />
                    <Button
                      tone='ghost'
                      size='xs'
                      composition='icon-only'
                      icon={<ArrowForwardRoundedIcon />}
                      tooltip={isPaused ? 'Send now' : 'Send now (stops the current response)'}
                      aria-label='Send now'
                      data-testid={`queued-message-send-now-${msg.id}`}
                      loading={isRowSending}
                      disabled={busy}
                      onClick={async (e) => {
                        e.stopPropagation();
                        setSendingMessageId(msg.id);
                        try {
                          await onSendNow(msg.id);
                        } catch {
                          /* The caller reports send failures. */
                        } finally {
                          setSendingMessageId(undefined);
                        }
                      }}
                    />
                  </Box>
                )}
              </Box>
            );
          })}
        </Box>
      )}
    </Box>
  );
};

export default QueuedMessagesBanner;
