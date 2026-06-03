import React, { useState, useMemo, useCallback, useEffect, useRef } from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import CheckIcon from '@mui/icons-material/Check';
import CloseIcon from '@mui/icons-material/Close';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';
import EditOutlinedIcon from '@mui/icons-material/EditOutlined';
import SearchIcon from '@mui/icons-material/Search';
import Tooltip from '@components1/ds/Tooltip';
import apiAskNudgebee from '@api1/ask-nudgebee';
import MarkDowns from '@components1/common/MarkDowns';
import { getNubiIconUrl } from '@hooks/useTenantBranding';
import { ds } from '@utils/colors';

const ZERO_UUID = '00000000-0000-0000-0000-000000000000';

const TYPE_GROUPS = {
  multi: ['multi_select'],
  single: ['single_select', 'tool_config', 'account_select'],
  input: ['text', 'user_input'],
};

// True only when the followup is *literally* yes/no — i.e. options contain both 'yes' and
// 'no' (case-insensitive). `tool_confirmation` alone isn't sufficient: backend could ship
// custom-labelled confirmations like ["Confirm","Cancel"], and rendering them as yn would
// either mis-label the buttons or submit the wrong string. Custom-labelled prompts fall
// back to single_select (numbered list), which is always safe.
const isYesNoFollowup = (options) => {
  if (!Array.isArray(options) || options.length !== 2) {
    return false;
  }
  const lower = options.map((o) => String(o).toLowerCase().trim());
  return lower.includes('yes') && lower.includes('no');
};

const resolveSheetType = (followupType, options) => {
  if (isYesNoFollowup(options)) {
    return 'yn';
  }
  if (TYPE_GROUPS.multi.includes(followupType)) {
    return 'multi';
  }
  if (TYPE_GROUPS.single.includes(followupType) || followupType === 'tool_confirmation') {
    return 'single';
  }
  return 'input';
};

// Heuristic — flag destructive-sounding prompts so the affirmative button can be styled
// red. Conservative on purpose: false negatives are safer than false positives here (a
// missed danger flag just keeps the default blue, while over-flagging would visually shout
// at the user for routine confirmations).
const DANGER_KEYWORDS = ['delete', 'remove', 'drop', 'terminate', 'destroy', 'kill', 'restart', 'reboot', 'shutdown', 'force', 'wipe'];
const isDangerQuestion = (question) => {
  if (!question) {
    return false;
  }
  const q = String(question).toLowerCase();
  return DANGER_KEYWORDS.some((k) => q.includes(k));
};

const Kbd = ({ children, sx }) => (
  <Box
    component='kbd'
    sx={{
      fontFamily: 'ui-monospace, "SF Mono", Menlo, monospace',
      fontWeight: 'var(--ds-font-weight-semibold)',
      fontSize: 'var(--ds-text-caption)',
      lineHeight: 1,
      background: 'var(--ds-background-200)',
      border: `1px solid ${'var(--ds-gray-200)'}`,
      borderRadius: ds.radius.sm,
      padding: `${ds.space[0]} ${ds.space[1]}`,
      color: 'var(--ds-gray-500)',
      ...sx,
    }}
  >
    {children}
  </Box>
);

Kbd.propTypes = { children: PropTypes.node, sx: PropTypes.object };

const ThreeDotsLoader = ({ size = 6, color, sx }) => (
  <Box
    sx={{
      display: 'inline-flex',
      alignItems: 'center',
      gap: `${Math.max(3, Math.round(size * 0.7))}px`,
      lineHeight: 0,
      '& > span': {
        width: `${size}px`,
        height: `${size}px`,
        borderRadius: '50%',
        background: color || 'currentColor',
        display: 'inline-block',
        animation: 'three-dots-pulse 1s infinite ease-in-out both',
      },
      '& > span:nth-of-type(1)': { animationDelay: '-0.32s' },
      '& > span:nth-of-type(2)': { animationDelay: '-0.16s' },
      '@keyframes three-dots-pulse': {
        '0%, 80%, 100%': { opacity: 0.15, transform: 'scale(0.6)' },
        '40%': { opacity: 1, transform: 'scale(1.15)' },
      },
      ...sx,
    }}
  >
    <span />
    <span />
    <span />
  </Box>
);

ThreeDotsLoader.propTypes = { size: PropTypes.number, color: PropTypes.string, sx: PropTypes.object };

const NubiBeeIcon = () => (
  <Box
    component='img'
    src={getNubiIconUrl()}
    alt='Nudgebee'
    sx={{ flexShrink: 0, width: ds.space.mul(0, 11), height: ds.space.mul(0, 11), display: 'block' }}
  />
);

const FollowupSheet = ({ followup, accountId, conversationId, selectedModel, popup, onStop, onSubmitted }) => {
  const messageConfig = useMemo(() => {
    if (!followup?.response?.message_config) {
      return {};
    }
    const mc = followup.response.message_config;
    if (typeof mc === 'string') {
      try {
        return JSON.parse(mc);
      } catch {
        return {};
      }
    }
    return mc;
  }, [followup]);

  const options = useMemo(() => messageConfig.followupOptions || [], [messageConfig]);
  const sheetType = resolveSheetType(messageConfig.followupType, options);
  const question = messageConfig.question || '';
  const isDanger = sheetType === 'yn' && isDangerQuestion(question);

  // For multi-paragraph or very long questions (e.g. clarification agent's "give me five
  // questions" style prompts) the head can blow up to dominate the entire sheet, making
  // the header strip indistinguishable from the body and pushing options out of view. In
  // that case we keep the head compact (icon + 1-line preview + X) and render the full
  // question inside the scrollable body instead.
  const isLongQuestion = Boolean(question) && (question.length > 200 || question.includes('\n'));
  const headPreview = useMemo(() => {
    if (!isLongQuestion) {
      return '';
    }
    const firstLine = question.split('\n').find((l) => l.trim()) || question;
    return firstLine.length > 100 ? firstLine.slice(0, 100).trim() + '…' : firstLine.trim();
  }, [isLongQuestion, question]);

  const [selectedMulti, setSelectedMulti] = useState([]);
  const [freeText, setFreeText] = useState('');
  const [textareaValue, setTextareaValue] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  // Captures what the user just submitted so the sheet can paint instant feedback (option
  // highlight / textarea lock) without waiting for the backend round-trip and poll cycle.
  const [pendingAnswer, setPendingAnswer] = useState(null);
  // Filter + keyboard cursor for option lists (single/multi). Filter input shows when there
  // are 6+ options so long lists are easy to narrow down.
  const [filter, setFilter] = useState('');
  const [cursor, setCursor] = useState(0);
  const textareaRef = useRef(null);
  const optionsContainerRef = useRef(null);

  const visibleOptions = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) {
      return options;
    }
    return options.filter((opt) => String(opt).toLowerCase().includes(q));
  }, [filter, options]);

  const showFilter = options.length >= 6 && (sheetType === 'single' || sheetType === 'multi');

  // When the active followup changes (new question arrives), reset local form state.
  // Backend can also rewrite an existing followup in-place (`GenerateFollowup` updates
  // `message_config` without changing message_id/agent_id when an active followup already
  // exists for the agent), so we also key on the question text + serialized options to
  // catch payload changes for the same followup id.
  const followupResetKey = useMemo(
    () => `${followup?.response?.message_id || ''}:${followup?.response?.agent_id || ''}:${question}:${JSON.stringify(options)}`,
    [followup?.response?.message_id, followup?.response?.agent_id, question, options]
  );
  useEffect(() => {
    setSelectedMulti([]);
    setFreeText('');
    setTextareaValue('');
    setIsSubmitting(false);
    setPendingAnswer(null);
    setFilter('');
    setCursor(0);
  }, [followupResetKey]);

  // Keep cursor inside visibleOptions bounds whenever the filter changes the list.
  useEffect(() => {
    if (visibleOptions.length === 0) {
      setCursor(0);
      return;
    }
    setCursor((c) => Math.min(c, visibleOptions.length - 1));
  }, [visibleOptions.length]);

  // Autosize the textarea for the input type.
  useEffect(() => {
    if (sheetType !== 'input' || !textareaRef.current) {
      return;
    }
    const ta = textareaRef.current;
    ta.style.height = 'auto';
    ta.style.height = Math.min(ta.scrollHeight, 220) + 'px';
  }, [textareaValue, sheetType]);

  // Auto-focus the textarea when an input-type sheet renders.
  useEffect(() => {
    if (sheetType === 'input' && textareaRef.current) {
      textareaRef.current.focus();
    }
  }, [sheetType, followup?.response?.message_id]);

  const submit = useCallback(
    async (payloadQuery) => {
      if (isSubmitting) {
        return;
      }
      // Paint the user's selection instantly before the network call goes out — the rest
      // (sheet hide, inline "You replied" pill) flows naturally through the polling cycle
      // once the followup transitions to COMPLETED on the server.
      setPendingAnswer(payloadQuery);
      setIsSubmitting(true);
      try {
        const agentId = followup?.response?.agent_id;
        const parentAgentId = followup?.response?.parent_agent_id;
        const resolvedParentAgentId = parentAgentId === ZERO_UUID || !parentAgentId ? agentId : parentAgentId;

        const requestPayload = {
          account_id: accountId || followup?.response?.account_id,
          query: payloadQuery,
          conversation_id: conversationId,
          message_id: followup?.response?.message_id,
          agent_id: agentId,
          parent_agent_id: resolvedParentAgentId,
        };

        if (selectedModel) {
          requestPayload.config = {
            llm_provider: selectedModel.provider,
            llm_model_name: selectedModel.model,
          };
        }

        await apiAskNudgebee.aiFollowupResponse(requestPayload);
        if (onSubmitted) {
          onSubmitted();
        }
        // Intentionally do NOT flip isSubmitting=false on success. The POST returns
        // in ~0.5–1.5s, but the *actual* visible effect — the next assistant message
        // / followup arriving via the polling cycle — lands several seconds later.
        // If we settled the loader on POST-return, the sheet would swap from spinner
        // to a static "Submitted/checkmark" state and just sit there, making the user
        // think nothing is happening. We let the parent unmount the sheet when the
        // followup transitions out of WAITING; that's the real "done" signal here.
      } catch (err) {
        // Rollback so the user can retry from the sheet.
        setPendingAnswer(null);
        setIsSubmitting(false);
        throw err;
      }
    },
    [accountId, conversationId, followup, isSubmitting, onSubmitted, selectedModel]
  );

  const submitSingle = useCallback(
    (option) => {
      submit(option);
    },
    [submit]
  );

  const submitMulti = useCallback(() => {
    if (selectedMulti.length === 0) {
      return;
    }
    submit(JSON.stringify(selectedMulti));
  }, [selectedMulti, submit]);

  const submitFreeText = useCallback(() => {
    const trimmed = freeText.trim();
    if (!trimmed) {
      return;
    }
    submit(trimmed);
  }, [freeText, submit]);

  const submitTextarea = useCallback(() => {
    const trimmed = textareaValue.trim();
    if (!trimmed) {
      return;
    }
    submit(trimmed);
  }, [textareaValue, submit]);

  const toggleMulti = useCallback((option) => {
    setSelectedMulti((prev) => (prev.includes(option) ? prev.filter((o) => o !== option) : [...prev, option]));
  }, []);

  const handleSelectAll = () => setSelectedMulti([...options]);
  const handleClear = () => setSelectedMulti([]);

  // The sheet is "locked" the moment a submission begins (pendingAnswer set) and stays
  // locked until the followup transitions to a terminal state (sheet unmounts) or the
  // active followup changes. This closes the race window where `isSubmitting` flips back
  // to false on API response but the polling-driven sheet-hide hasn't fired yet — without
  // this, the user could click a second option in that ~500ms-1.5s gap and send a
  // duplicate response to the backend.
  const isLocked = isSubmitting || pendingAnswer !== null;

  // Keyboard navigation:
  //  - single/multi: ↑/↓ to move cursor, Enter to submit (single) / confirm (multi),
  //    Space to toggle (multi).
  //  - yn: Y/N keys submit the corresponding option directly.
  // Skips when user is typing in any input/textarea (filter, freetext, send-textarea),
  // when no options are interactive, or while a submission is already in flight. ESC is
  // intentionally not bound — backend has no "skip followup" semantic, so binding it
  // would silently do nothing and feel broken.
  useEffect(() => {
    if (!followup || pendingAnswer) {
      return;
    }
    if (sheetType !== 'single' && sheetType !== 'multi' && sheetType !== 'yn') {
      return;
    }
    const handler = (e) => {
      const tag = (e.target?.tagName || '').toLowerCase();
      const isEditable = tag === 'input' || tag === 'textarea' || e.target?.isContentEditable;
      if (isEditable) {
        return;
      }
      if (sheetType === 'yn') {
        if (e.key === 'y' || e.key === 'Y') {
          e.preventDefault();
          submitSingle(options.find((o) => String(o).toLowerCase().trim() === 'yes') || 'yes');
        } else if (e.key === 'n' || e.key === 'N') {
          e.preventDefault();
          submitSingle(options.find((o) => String(o).toLowerCase().trim() === 'no') || 'no');
        }
        return;
      }
      if (visibleOptions.length === 0) {
        return;
      }
      if (e.key === 'ArrowDown') {
        e.preventDefault();
        setCursor((c) => Math.min(visibleOptions.length - 1, c + 1));
      } else if (e.key === 'ArrowUp') {
        e.preventDefault();
        setCursor((c) => Math.max(0, c - 1));
      } else if (e.key === 'Enter') {
        if (sheetType === 'single') {
          e.preventDefault();
          const opt = visibleOptions[cursor];
          if (opt) {
            submitSingle(opt);
          }
        } else if (sheetType === 'multi' && selectedMulti.length > 0) {
          e.preventDefault();
          submitMulti();
        }
      } else if (e.key === ' ' && sheetType === 'multi') {
        e.preventDefault();
        const opt = visibleOptions[cursor];
        if (opt) {
          toggleMulti(opt);
        }
      }
    };
    document.addEventListener('keydown', handler);
    return () => document.removeEventListener('keydown', handler);
  }, [followup, sheetType, options, visibleOptions, cursor, pendingAnswer, selectedMulti, submitSingle, submitMulti, toggleMulti]);

  // Scroll the cursor option into view when it changes via keyboard.
  useEffect(() => {
    if (!optionsContainerRef.current) {
      return;
    }
    const el = optionsContainerRef.current.querySelector(`[data-opt-index="${cursor}"]`);
    if (el && el.scrollIntoView) {
      el.scrollIntoView({ block: 'nearest' });
    }
  }, [cursor]);

  if (!followup) {
    return null;
  }

  const optionRowSx = {
    display: 'flex',
    alignItems: 'center',
    gap: ds.space.mul(0, 5),
    padding: `${ds.space[2]} ${ds.space.mul(0, 5)}`,
    borderRadius: ds.radius.lg,
    border: '1px solid transparent',
    cursor: 'pointer',
    textAlign: 'left',
    width: '100%',
    background: 'transparent',
    transition: 'background 0.1s, border-color 0.1s',
    fontSize: 'var(--ds-text-body)',
    fontWeight: 'var(--ds-font-weight-medium)',
    lineHeight: 1.3,
    letterSpacing: '-0.005em',
    color: 'var(--ds-gray-700)',
    '&:hover': {
      background: 'var(--ds-blue-100)',
      borderColor: 'var(--ds-blue-200)',
    },
  };

  const renderHead = () => (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'center',
        gap: ds.space[3],
        padding: `${ds.space[3]} ${ds.space.mul(0, 7)}`,
        // Subtle cool grey instead of blue tint — keeps the head distinguishable from
        // the body without the previous "blue banner" feel.
        background: 'var(--ds-background-200)',
        borderBottom: '1px solid var(--ds-brand-100)',
      }}
    >
      <NubiBeeIcon />
      {question && !isLongQuestion ? (
        <Box
          sx={{
            flex: 1,
            minWidth: 0,
            display: 'flex',
            alignItems: 'center',
            fontSize: 'var(--ds-text-body-lg)',
            lineHeight: 1.5,
            letterSpacing: '-0.005em',
            wordBreak: 'break-word',
            // Dark slate (not primary blue) + medium weight so the question reads as a
            // calm conversation message instead of a shouty header.
            color: 'var(--ds-gray-800)',
            '&&, && *': {
              color: 'var(--ds-gray-800)',
              fontWeight: 'var(--ds-font-weight-medium)',
              margin: 0,
              padding: 0,
            },
            '& code': {
              fontFamily: 'ui-monospace, "SF Mono", Menlo, monospace',
              fontSize: 'var(--ds-text-body)',
              background: 'var(--ds-background-100)',
              padding: `${ds.space[0]} ${ds.space.mul(0, 3)} !important`,
              borderRadius: ds.radius.sm,
              border: `1px solid ${'var(--ds-gray-200)'}`,
            },
          }}
        >
          <MarkDowns data={question} />
        </Box>
      ) : isLongQuestion ? (
        <Box
          sx={{
            flex: 1,
            minWidth: 0,
            fontSize: 'var(--ds-text-body-lg)',
            fontWeight: 'var(--ds-font-weight-medium)',
            lineHeight: 1.4,
            color: 'var(--ds-brand-600)',
            // Single-line truncated preview — full question lives in the scrollable body
            // just below the head.
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          }}
        >
          {headPreview}
        </Box>
      ) : (
        <Box sx={{ flex: 1 }} />
      )}
      {onStop && (
        <Tooltip title='Stop conversation' placement='top'>
          <Box
            component='button'
            type='button'
            onClick={onStop}
            aria-label='Stop conversation'
            sx={{
              flexShrink: 0,
              width: ds.space.mul(0, 11),
              height: ds.space.mul(0, 11),
              borderRadius: ds.radius.sm,
              border: 'none',
              background: 'transparent',
              color: 'var(--ds-gray-500)',
              cursor: 'pointer',
              display: 'grid',
              placeItems: 'center',
              padding: 0,
              transition: 'all 0.12s',
              '&:hover': { color: 'var(--ds-red-600)', background: 'rgba(220,38,38,0.08)' },
            }}
          >
            <CloseIcon sx={{ fontSize: 'var(--ds-text-body-lg)' }} />
          </Box>
        </Tooltip>
      )}
    </Box>
  );

  const renderFilter = () => (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'center',
        gap: ds.space[2],
        margin: `0 0 ${ds.space.mul(0, 5)}`,
        padding: `${ds.space.mul(0, 3)} ${ds.space[3]}`,
        background: 'var(--ds-background-200)',
        border: `1px solid ${'var(--ds-gray-200)'}`,
        borderRadius: ds.radius.lg,
      }}
    >
      <SearchIcon sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-gray-500)', flexShrink: 0 }} />
      <Box
        component='input'
        type='text'
        placeholder='Filter options…'
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
        disabled={isLocked}
        sx={{
          flex: 1,
          border: 'none',
          outline: 'none',
          background: 'transparent',
          fontSize: 'var(--ds-text-body)',
          fontWeight: 'var(--ds-font-weight-regular)',
          letterSpacing: '-0.005em',
          color: 'var(--ds-brand-600)',
          fontFamily: 'inherit',
          padding: 0,
          '&::placeholder': { color: 'var(--ds-gray-500)' },
        }}
      />
      {filter && (
        <Box
          component='span'
          sx={{
            fontSize: 'var(--ds-text-caption)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: 'var(--ds-gray-500)',
            flexShrink: 0,
            fontVariantNumeric: 'tabular-nums',
          }}
        >
          {visibleOptions.length} of {options.length}
        </Box>
      )}
    </Box>
  );

  const renderFreetextRow = () => (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'center',
        gap: ds.space.mul(0, 5),
        margin: `${ds.space[2]} 0 ${ds.space[1]}`,
        padding: `${ds.space[2]} ${ds.space.mul(0, 5)}`,
        borderTop: `1px dashed ${'var(--ds-gray-200)'}`,
      }}
    >
      <EditOutlinedIcon sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-gray-500)', flexShrink: 0 }} />
      <Box
        component='input'
        type='text'
        placeholder='Something else…'
        value={freeText}
        disabled={isLocked}
        onChange={(e) => setFreeText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && freeText.trim()) {
            e.preventDefault();
            submitFreeText();
          }
        }}
        sx={{
          flex: 1,
          border: 'none',
          outline: 'none',
          background: 'transparent',
          fontSize: 'var(--ds-text-body)',
          fontWeight: 'var(--ds-font-weight-regular)',
          lineHeight: 1.3,
          letterSpacing: '-0.005em',
          padding: `${ds.space[0]} 0`,
          color: 'var(--ds-brand-600)',
          fontFamily: 'inherit',
          '&::placeholder': { color: 'var(--ds-gray-500)' },
        }}
      />
      {!freeText.trim() && !pendingAnswer && (
        <Box
          sx={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: ds.space.mul(0, 5),
            flexShrink: 0,
            fontSize: 'var(--ds-text-caption)',
            fontWeight: 'var(--ds-font-weight-medium)',
            color: 'var(--ds-gray-500)',
            letterSpacing: '-0.005em',
          }}
        >
          <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1] }}>
            <Kbd>↑</Kbd>
            <Kbd>↓</Kbd>
          </Box>
          {sheetType === 'multi' && (
            <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1] }}>
              <Kbd>Space</Kbd>
            </Box>
          )}
          <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1] }}>
            <Kbd>↵</Kbd>
          </Box>
        </Box>
      )}
      {freeText.trim() && (
        <Box
          component='button'
          type='button'
          onClick={submitFreeText}
          disabled={isLocked}
          aria-label='Send'
          sx={{
            flexShrink: 0,
            width: ds.space[5],
            height: ds.space[5],
            borderRadius: ds.radius.sm,
            border: 'none',
            background: 'var(--ds-blue-600)',
            color: 'var(--ds-background-100)',
            display: 'grid',
            placeItems: 'center',
            cursor: isSubmitting ? 'not-allowed' : 'pointer',
            padding: 0,
            transition: 'background 0.1s',
            opacity: isSubmitting ? 0.85 : 1,
            '&:hover': { background: 'var(--ds-blue-600)' },
          }}
        >
          {pendingAnswer && isSubmitting ? (
            <ThreeDotsLoader size={4} color={'var(--ds-background-100)'} />
          ) : (
            <ArrowForwardIcon sx={{ fontSize: 'var(--ds-text-body)' }} />
          )}
        </Box>
      )}
    </Box>
  );

  const renderSingle = () => (
    <Box
      ref={optionsContainerRef}
      sx={{
        display: 'flex',
        flexDirection: 'column',
        gap: ds.space[0],
        maxHeight: ds.space.mul(5, 10),
        overflow: 'auto',
        margin: '0 -2px',
        padding: `0 ${ds.space[0]}`,
      }}
    >
      {visibleOptions.length === 0 && (
        <Box
          sx={{ padding: `${ds.space[3]} ${ds.space.mul(0, 5)}`, fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-500)', fontStyle: 'italic' }}
        >
          No matches
        </Box>
      )}
      {visibleOptions.map((option, i) => {
        const isPending = pendingAnswer === option;
        const isOtherPending = pendingAnswer !== null && !isPending;
        const isFocused = i === cursor && !pendingAnswer;
        return (
          <Box
            key={option}
            data-opt-index={i}
            component='button'
            type='button'
            disabled={isLocked}
            onClick={() => submitSingle(option)}
            onMouseEnter={() => setCursor(i)}
            sx={{
              ...optionRowSx,
              ...(isFocused && {
                background: 'var(--ds-blue-100)',
                borderColor: 'var(--ds-blue-200)',
              }),
              ...(isPending && {
                background: 'var(--ds-blue-100)',
                borderColor: 'var(--ds-blue-600)',
                color: 'var(--ds-blue-600)',
              }),
              ...(isOtherPending && {
                opacity: 0.4,
              }),
            }}
          >
            <Box
              sx={{
                flexShrink: 0,
                width: ds.space.mul(0, 9),
                height: ds.space.mul(0, 9),
                borderRadius: ds.radius.sm,
                background: isPending || isFocused ? 'var(--ds-blue-600)' : 'var(--ds-background-200)',
                border: `1px solid ${isPending || isFocused ? 'var(--ds-blue-600)' : 'var(--ds-gray-200)'}`,
                display: 'grid',
                placeItems: 'center',
                fontFamily: 'ui-monospace, "SF Mono", Menlo, monospace',
                fontWeight: 'var(--ds-font-weight-semibold)',
                fontSize: 'var(--ds-text-caption)',
                lineHeight: 1,
                color: isPending || isFocused ? 'var(--ds-background-100)' : 'var(--ds-gray-500)',
                transition: 'all 0.1s',
              }}
            >
              {isPending ? <CheckIcon sx={{ fontSize: 'var(--ds-text-caption)' }} /> : i + 1}
            </Box>
            <Box sx={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontWeight: isPending ? 600 : 500 }}>
              {option}
            </Box>
            {isPending && isSubmitting ? (
              <ThreeDotsLoader color={'var(--ds-blue-600)'} sx={{ flexShrink: 0, mr: ds.space[1] }} />
            ) : (
              <ArrowForwardIcon
                sx={{
                  fontSize: 'var(--ds-text-small)',
                  color: isPending || isFocused ? 'var(--ds-blue-600)' : 'transparent',
                }}
              />
            )}
          </Box>
        );
      })}
    </Box>
  );

  const renderMulti = () => (
    <Box
      ref={optionsContainerRef}
      sx={{
        display: 'flex',
        flexDirection: 'column',
        gap: ds.space[0],
        maxHeight: ds.space.mul(5, 10),
        overflow: 'auto',
        margin: '0 -2px',
        padding: `0 ${ds.space[0]}`,
      }}
    >
      {visibleOptions.length === 0 && (
        <Box
          sx={{ padding: `${ds.space[3]} ${ds.space.mul(0, 5)}`, fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-500)', fontStyle: 'italic' }}
        >
          No matches
        </Box>
      )}
      {visibleOptions.map((option, i) => {
        const isSelected = selectedMulti.includes(option);
        const isFocused = i === cursor && !pendingAnswer;
        return (
          <Box
            key={option}
            data-opt-index={i}
            component='button'
            type='button'
            disabled={isLocked}
            onClick={() => toggleMulti(option)}
            onMouseEnter={() => setCursor(i)}
            sx={{
              ...optionRowSx,
              ...(isFocused &&
                !isSelected && {
                  background: 'var(--ds-blue-100)',
                  borderColor: 'var(--ds-blue-200)',
                }),
              ...(isSelected && {
                background: 'var(--ds-blue-100)',
                borderColor: isFocused ? 'var(--ds-blue-600)' : 'var(--ds-blue-200)',
              }),
            }}
          >
            <Box
              sx={{
                flexShrink: 0,
                width: ds.space.mul(0, 7),
                height: ds.space.mul(0, 7),
                borderRadius: ds.radius.sm,
                border: `1.5px solid ${isSelected ? 'var(--ds-blue-600)' : 'var(--ds-gray-200)'}`,
                background: isSelected ? 'var(--ds-blue-600)' : 'var(--ds-background-100)',
                display: 'grid',
                placeItems: 'center',
                transition: 'all 0.1s',
              }}
            >
              {isSelected && <CheckIcon sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-background-100)' }} />}
            </Box>
            <Box sx={{ flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{option}</Box>
          </Box>
        );
      })}
    </Box>
  );

  const renderMultiFooter = () => (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'center',
        gap: ds.space.mul(0, 5),
        padding: `${ds.space[2]} ${ds.space.mul(0, 7)} ${ds.space[3]}`,
        borderTop: `1px solid ${'var(--ds-gray-200)'}`,
      }}
    >
      <Box
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: ds.space.mul(0, 3),
          fontSize: 'var(--ds-text-caption)',
          color: 'var(--ds-gray-500)',
          fontWeight: 'var(--ds-font-weight-medium)',
        }}
      >
        <Box
          sx={{
            width: ds.space[1],
            height: ds.space[1],
            borderRadius: ds.radius.pill,
            background: selectedMulti.length > 0 ? 'var(--ds-blue-600)' : 'var(--ds-gray-200)',
          }}
        />
        <Box
          component='strong'
          sx={{ color: selectedMulti.length > 0 ? 'var(--ds-blue-600)' : 'var(--ds-gray-500)', fontWeight: 'var(--ds-font-weight-semibold)' }}
        >
          {selectedMulti.length}
        </Box>
        of {options.length} selected
      </Box>
      <Box
        component='button'
        type='button'
        onClick={handleSelectAll}
        disabled={isLocked}
        sx={{
          border: 'none',
          background: 'transparent',
          cursor: 'pointer',
          fontSize: 'var(--ds-text-caption)',
          fontWeight: 'var(--ds-font-weight-medium)',
          color: 'var(--ds-gray-500)',
          padding: `${ds.space[1]} ${ds.space.mul(0, 3)}`,
          borderRadius: ds.radius.sm,
          transition: 'all 0.1s',
          '&:hover': { color: 'var(--ds-blue-600)', background: 'var(--ds-blue-100)' },
        }}
      >
        Select all
      </Box>
      <Box
        component='button'
        type='button'
        onClick={handleClear}
        disabled={isLocked}
        sx={{
          border: 'none',
          background: 'transparent',
          cursor: 'pointer',
          fontSize: 'var(--ds-text-caption)',
          fontWeight: 'var(--ds-font-weight-medium)',
          color: 'var(--ds-gray-500)',
          padding: `${ds.space[1]} ${ds.space.mul(0, 3)}`,
          borderRadius: ds.radius.sm,
          transition: 'all 0.1s',
          '&:hover': { color: 'var(--ds-blue-600)', background: 'var(--ds-blue-100)' },
        }}
      >
        Clear
      </Box>
      <Box sx={{ flex: 1 }} />
      <Box
        component='button'
        type='button'
        onClick={submitMulti}
        disabled={isLocked || selectedMulti.length === 0}
        sx={{
          display: 'inline-flex',
          alignItems: 'center',
          gap: ds.space.mul(0, 3),
          padding: `${ds.space.mul(0, 3)} ${ds.space[3]} ${ds.space.mul(0, 3)} ${ds.space.mul(0, 7)}`,
          borderRadius: ds.radius.md,
          border: 'none',
          background: selectedMulti.length === 0 ? 'var(--ds-background-200)' : 'var(--ds-blue-600)',
          color: selectedMulti.length === 0 ? 'var(--ds-gray-500)' : 'var(--ds-background-100)',
          fontWeight: 'var(--ds-font-weight-semibold)',
          fontSize: 'var(--ds-text-small)',
          letterSpacing: '-0.005em',
          cursor: selectedMulti.length === 0 || isSubmitting ? 'not-allowed' : 'pointer',
          transition: 'background 0.1s',
          opacity: isSubmitting ? 0.7 : 1,
          '&:hover': {
            background: selectedMulti.length === 0 ? 'var(--ds-background-200)' : 'var(--ds-blue-600)',
          },
        }}
      >
        {pendingAnswer && isSubmitting ? <ThreeDotsLoader size={4} color={'var(--ds-background-100)'} /> : null}
        {pendingAnswer && !isSubmitting ? <CheckIcon sx={{ fontSize: 'var(--ds-text-body)' }} /> : null}
        {pendingAnswer ? (isSubmitting ? 'Submitting' : 'Submitted') : 'Continue'}
      </Box>
    </Box>
  );

  const renderYn = () => {
    // Backend stores the user's choice as the literal option string. Most agents send
    // ["yes","no"] (lowercase) but we also see ["Yes","No"]; preserve the case the
    // backend sent so the response matches what was offered.
    // isYesNoFollowup() guarantees both options exist, so find() never falls back —
    // but defaulting to lower-case 'yes'/'no' is a safe last resort.
    const yesOption = options.find((o) => String(o).toLowerCase().trim() === 'yes') || 'yes';
    const noOption = options.find((o) => String(o).toLowerCase().trim() === 'no') || 'no';
    // Display the literal option string the backend sent ("Yes" vs "yes"), so the UI
    // matches whatever convention the agent used.
    const noLabel = noOption;
    const yesLabel = isDanger ? `${yesOption}, continue` : yesOption;
    const yesPending = pendingAnswer === yesOption;
    const noPending = pendingAnswer === noOption;
    const someonePending = pendingAnswer !== null;

    const yesBg = isDanger ? '#dc2626' : 'var(--ds-blue-600)';
    const yesBgHover = isDanger ? '#b91c1c' : 'var(--ds-blue-600)';

    return (
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], padding: `${ds.space.mul(0, 7)} ${ds.space.mul(0, 7)}` }}>
        <Box
          component='button'
          type='button'
          disabled={someonePending}
          onClick={() => submitSingle(noOption)}
          sx={{
            display: 'inline-flex',
            alignItems: 'center',
            justifyContent: 'center',
            gap: ds.space[2],
            padding: `${ds.space[2]} ${ds.space[4]}`,
            minWidth: ds.space.mul(1, 25),
            borderRadius: ds.radius.lg,
            border: `1px solid ${'var(--ds-gray-200)'}`,
            background: 'var(--ds-background-100)',
            color: 'var(--ds-blue-500)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            fontSize: 'var(--ds-text-body)',
            letterSpacing: '-0.005em',
            cursor: someonePending ? 'not-allowed' : 'pointer',
            opacity: someonePending && !noPending ? 0.4 : 1,
            transition: 'all 0.12s',
            '&:hover': someonePending
              ? {}
              : {
                  borderColor: 'var(--ds-gray-500)',
                  background: 'var(--ds-background-200)',
                },
          }}
        >
          {noPending && isSubmitting ? (
            <ThreeDotsLoader size={4} color={'var(--ds-blue-500)'} />
          ) : noPending ? (
            <CheckIcon sx={{ fontSize: 'var(--ds-text-body-lg)' }} />
          ) : null}
          {noLabel}
        </Box>
        <Box
          component='button'
          type='button'
          disabled={someonePending}
          onClick={() => submitSingle(yesOption)}
          sx={{
            display: 'inline-flex',
            alignItems: 'center',
            justifyContent: 'center',
            gap: ds.space[2],
            padding: `${ds.space[2]} ${ds.space.mul(0, 9)}`,
            minWidth: ds.space.mul(1, 35),
            borderRadius: ds.radius.lg,
            border: `1px solid ${yesBg}`,
            background: yesBg,
            color: 'var(--ds-background-100)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            fontSize: 'var(--ds-text-body)',
            letterSpacing: '-0.005em',
            cursor: someonePending ? 'not-allowed' : 'pointer',
            opacity: someonePending && !yesPending ? 0.4 : 1,
            transition: 'all 0.12s',
            '&:hover': someonePending
              ? {}
              : {
                  background: yesBgHover,
                  borderColor: yesBgHover,
                },
          }}
        >
          {yesPending && isSubmitting ? (
            <ThreeDotsLoader size={4} color={'var(--ds-background-100)'} />
          ) : yesPending ? (
            <CheckIcon sx={{ fontSize: 'var(--ds-text-body-lg)' }} />
          ) : null}
          {yesLabel}
        </Box>
        <Box sx={{ flex: 1 }} />
        {!pendingAnswer && (
          <Box
            sx={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: ds.space[2],
              fontSize: 'var(--ds-text-caption)',
              fontWeight: 'var(--ds-font-weight-medium)',
              color: 'var(--ds-gray-500)',
              letterSpacing: '-0.005em',
            }}
          >
            <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1] }}>
              <Kbd>Y</Kbd>
            </Box>
            <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1] }}>
              <Kbd>N</Kbd>
            </Box>
          </Box>
        )}
      </Box>
    );
  };

  const renderInput = () => (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space.mul(0, 3), padding: `0 ${ds.space.mul(0, 7)} ${ds.space[3]}` }}>
      <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: ds.space[2], padding: `${ds.space[0]} 0` }}>
        <Box
          component='textarea'
          ref={textareaRef}
          rows={1}
          maxLength={550}
          placeholder='Type your response…'
          value={textareaValue}
          disabled={isLocked}
          onChange={(e) => setTextareaValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
              e.preventDefault();
              submitTextarea();
            }
          }}
          sx={{
            flex: 1,
            border: 'none',
            outline: 'none',
            background: 'transparent',
            resize: 'none',
            fontSize: 'var(--ds-text-body)',
            fontWeight: 'var(--ds-font-weight-regular)',
            lineHeight: 1.5,
            letterSpacing: '-0.005em',
            padding: `${ds.space[0]} 0`,
            minHeight: ds.space.mul(0, 11),
            maxHeight: ds.space.mul(4, 10),
            // Theme's text.primary resolves to blue in this app — hardcode dark slate so
            // the typed response reads as plain text instead of a hyperlink-looking blue.
            color: 'var(--ds-brand-600)',
            fontFamily: 'inherit',
            '&::placeholder': { color: 'var(--ds-gray-500)' },
          }}
        />
        <Box
          component='button'
          type='button'
          onClick={submitTextarea}
          disabled={isLocked || !textareaValue.trim()}
          aria-label='Send'
          sx={{
            flexShrink: 0,
            width: ds.space.mul(1, 7),
            height: ds.space.mul(1, 7),
            borderRadius: ds.radius.lg,
            border: 'none',
            background: pendingAnswer
              ? 'var(--ds-blue-600)'
              : textareaValue.trim() && !isSubmitting
              ? 'var(--ds-blue-600)'
              : 'var(--ds-background-200)',
            color: pendingAnswer
              ? 'var(--ds-background-100)'
              : textareaValue.trim() && !isSubmitting
              ? 'var(--ds-background-100)'
              : 'var(--ds-gray-500)',
            display: 'grid',
            placeItems: 'center',
            cursor: textareaValue.trim() && !isSubmitting ? 'pointer' : 'not-allowed',
            padding: 0,
            alignSelf: 'flex-end',
            transition: 'all 0.12s',
            opacity: isSubmitting ? 0.85 : 1,
            '&:hover': {
              background: textareaValue.trim() && !isSubmitting ? 'var(--ds-blue-600)' : 'var(--ds-background-200)',
            },
          }}
        >
          {pendingAnswer && isSubmitting ? (
            <ThreeDotsLoader size={4} color={'var(--ds-background-100)'} />
          ) : pendingAnswer ? (
            <CheckIcon sx={{ fontSize: 'var(--ds-text-body-lg)' }} />
          ) : (
            <ArrowForwardIcon sx={{ fontSize: 'var(--ds-text-body)' }} />
          )}
        </Box>
      </Box>
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: ds.space.mul(0, 3),
          fontSize: 'var(--ds-text-caption)',
          fontWeight: 'var(--ds-font-weight-medium)',
          color: 'var(--ds-gray-500)',
          padding: `0 ${ds.space[0]}`,
          letterSpacing: '-0.005em',
        }}
      >
        <Box>
          Press <Kbd>↵</Kbd> to send · <Kbd>Shift</Kbd>+<Kbd>↵</Kbd> for newline
        </Box>
        <Box sx={{ flex: 1 }} />
        <Box sx={{ fontVariantNumeric: 'tabular-nums' }}>{textareaValue.length} / 550</Box>
      </Box>
    </Box>
  );

  return (
    <Box
      sx={{
        position: 'relative',
        background: 'var(--ds-background-100)',
        // Calm neutral chrome — primary blue is reserved for actions and the just-clicked
        // option, so the sheet feels like a quiet card instead of a blue billboard.
        border: '1px solid var(--ds-gray-200)',
        borderRadius: ds.radius.xl,
        // Stronger upward-cast shadow so the sheet visibly floats above the conversation
        // page instead of blending with the (now-neutral) background.
        boxShadow: '0 -12px 40px -16px rgba(15,23,42,.18), 0 -4px 16px -6px rgba(15,23,42,.10), 0 1px 2px rgba(15,23,42,.06)',
        overflow: 'hidden',
        animation: 'sheetUp 0.32s cubic-bezier(0.2, 0.7, 0.3, 1) both',
        '@keyframes sheetUp': {
          from: { opacity: 0, transform: 'translateY(10px)' },
          to: { opacity: 1, transform: 'translateY(0)' },
        },
        // Cap the sheet so it never overflows the viewport / sidebar. Tighter cap in popup
        // mode because NubiChatSidebar embeds (workflow builder, optimize tab) are typically
        // narrower vertically than a full page. Body inside becomes scrollable beyond the cap
        // while the head and any footer stay anchored.
        maxHeight: popup ? 'min(50vh, 380px)' : 'calc(100vh - 160px)',
        display: 'flex',
        flexDirection: 'column',
      }}
    >
      {renderHead()}
      {(isLongQuestion || sheetType === 'single' || sheetType === 'multi') && (
        <Box
          sx={{
            padding: `${ds.space[3]} ${ds.space.mul(0, 7)} ${ds.space[1]}`,
            flex: '1 1 auto',
            minHeight: 0,
            overflowY: 'auto',
            // Hide the inner scrollbar entirely so it doesn't visually clash with the page
            // scrollbar; mouse-wheel and trackpad scroll still work for navigating long prompts.
            '&::-webkit-scrollbar': { display: 'none', width: 0, height: 0 },
            '&::-webkit-scrollbar-track': { display: 'none' },
            '&::-webkit-scrollbar-thumb': { display: 'none' },
            scrollbarWidth: 'none',
            msOverflowStyle: 'none',
            // Soft fade at the bottom of the scrollable area as a visual cue that there's
            // more content to scroll to.
            maskImage: 'linear-gradient(to bottom, black calc(100% - 12px), transparent 100%)',
            WebkitMaskImage: 'linear-gradient(to bottom, black calc(100% - 12px), transparent 100%)',
          }}
        >
          {isLongQuestion && (
            <Box
              sx={{
                fontSize: 'var(--ds-text-body-lg)',
                fontWeight: 'var(--ds-font-weight-medium)',
                lineHeight: 1.6,
                color: 'var(--ds-brand-600)',
                wordBreak: 'break-word',
                marginBottom: sheetType === 'single' || sheetType === 'multi' ? ds.space.mul(0, 7) : '0px',
                paddingBottom: sheetType === 'single' || sheetType === 'multi' ? ds.space[3] : '0px',
                borderBottom: sheetType === 'single' || sheetType === 'multi' ? '1px solid #EDF0F4' : 'none',
                '& p': { margin: `0 0 ${ds.space[2]}` },
                '& p:last-child': { margin: 0 },
                '& code': {
                  fontFamily: 'ui-monospace, "SF Mono", Menlo, monospace',
                  fontSize: 'var(--ds-text-body)',
                  background: 'var(--ds-background-200)',
                  padding: `${ds.space[0]} ${ds.space.mul(0, 3)}`,
                  borderRadius: ds.radius.sm,
                  border: `1px solid ${'var(--ds-gray-200)'}`,
                },
                '& ul, & ol': { paddingLeft: ds.space.mul(1, 5), margin: `0 0 ${ds.space[2]}` },
                '& li': { marginBottom: ds.space[0] },
                '& strong': { fontWeight: 'var(--ds-font-weight-semibold)' },
              }}
            >
              <MarkDowns data={question} />
            </Box>
          )}
          {showFilter && renderFilter()}
          {sheetType === 'single' && renderSingle()}
          {sheetType === 'multi' && renderMulti()}
          {(sheetType === 'single' || sheetType === 'multi') && renderFreetextRow()}
        </Box>
      )}
      {sheetType === 'multi' && renderMultiFooter()}
      {sheetType === 'input' && renderInput()}
      {sheetType === 'yn' && renderYn()}
    </Box>
  );
};

FollowupSheet.propTypes = {
  followup: PropTypes.shape({
    response: PropTypes.shape({
      message_id: PropTypes.string,
      agent_id: PropTypes.string,
      parent_agent_id: PropTypes.string,
      account_id: PropTypes.string,
      message_config: PropTypes.oneOfType([PropTypes.string, PropTypes.object]),
      status: PropTypes.string,
    }),
  }).isRequired,
  accountId: PropTypes.string,
  conversationId: PropTypes.string,
  selectedModel: PropTypes.shape({
    provider: PropTypes.string,
    model: PropTypes.string,
  }),
  popup: PropTypes.bool,
  onStop: PropTypes.func,
  onSubmitted: PropTypes.func,
};

export default FollowupSheet;
