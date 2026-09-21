import React, { useEffect, useMemo, useRef, useState, useCallback } from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import MessageItem from './MessageItem';
import CustomDrawer, { SecondaryDrawer } from '@shared/CustomDrawer';
import TasksDrawerContent from './common/TasksDrawerContent';
import MemoriesDrawerContent from './common/MemoriesDrawerContent';
import ReferencesDrawerContent from './common/ReferencesDrawerContent';
import ChannelContextDrawerContent from './common/ChannelContextDrawerContent';
import ToolDetails from './common/ToolDetails';
import useMessageAdditionalData from '@hooks/useMessageAdditionalData';
import WatchesTab from './WatchesTab';
import api from '@api1/ask-nudgebee';
import { useWatchFeatureEnabled } from '@hooks/useTenantBranding';
import { TERMINAL_WATCH_STATUSES, WATCH_FOLLOWUP_BUDGET_MS, countAwaitingFollowups, reconcilePendingFollowups } from './utils/watchFollowup';
import { actionableTasks } from './utils/taskClassification';

const taskKey = (task) => task?.id ?? task?.tool_id ?? task?.originalIndex ?? null;

const MessageStream = ({ messages, isProcessing, collapsedObj, setCollapsedObj, showFullText, setShowFullText, itemProps }) => {
  // Primary right drawer — Tasks list, Contexts table, Memories, or (for inline rows) Tool Details.
  // `kind` drives which content renderer runs so the panel re-renders when `secondary` changes
  // (we can't store JSX directly because the active-task highlight needs to update live).
  const [drawer, setDrawer] = useState({ open: false, kind: null, title: '', data: null });
  // Secondary panel — opens flush against the left edge of the primary drawer to show
  // ToolDetails for the task selected inside the Tasks list (replaces the old in-row accordion).
  const [secondary, setSecondary] = useState({ open: false, task: null });
  const [primaryWidth, setPrimaryWidth] = useState(0);

  const closeSecondary = useCallback(() => setSecondary({ open: false, task: null }), []);
  const closeDrawer = useCallback(() => {
    setDrawer((d) => ({ ...d, open: false }));
    setSecondary({ open: false, task: null });
  }, []);

  const openSecondaryToolDetails = useCallback((task) => {
    setSecondary({ open: true, task });
  }, []);

  const groupedMessages = useMemo(() => {
    const groups = [];
    let currentGroup = null;
    messages.forEach((m, index) => {
      const type = m.tool ?? m.type;
      if (type === 'question') {
        if (currentGroup) {
          groups.push(currentGroup);
        }
        currentGroup = { question: { ...m, originalIndex: index }, children: [] };
      } else if (currentGroup) {
        currentGroup.children.push({ ...m, originalIndex: index });
      }
    });
    if (currentGroup) {
      groups.push(currentGroup);
    }
    return groups;
  }, [messages]);

  const additionalData = useMessageAdditionalData(groupedMessages, itemProps.accountId, itemProps.conversationId);

  // Count of watches registered against this conversation. Conversation-level
  // (not per-response), so we fetch once and surface on the last response's
  // meta-rail. Refresh ticker fires every 5s so newly-registered watches
  // show up promptly without needing a full conversation reload — see
  // PR #30040 / #30216 for the watch feature it visualises.
  // Poll only while something might still change:
  //   - the conversation is still processing (agent might register a new watch), or
  //   - at least one existing watch is non-terminal (PENDING/ACTIVE — its status
  //     transitions need to flow into the chip)
  // Once the conversation is settled AND all watches are in a terminal state, stop
  // polling — otherwise the network tab fills with `ListWatchesByConversation`
  // calls forever for chats with no live watches at all.
  const [watchCount, setWatchCount] = useState(0);
  // Gate all watch polling on the per-env feature flag (LLM_SERVER_WATCH_ENABLED,
  // surfaced via /api/public/app_config). When off, llm-server never mounts the
  // /v1/watches route, so we skip the poll entirely instead of 404-ing on it.
  const watchFeatureEnabled = useWatchFeatureEnabled();
  // Watches that have gone terminal but whose follow-up block has not shown
  // up in the loaded messages yet: watch id -> deadline (epoch ms). Drives
  // both the re-fetch trigger and whether the poller keeps ticking. See
  // ./utils/watchFollowup for why a terminal status is not enough on its own.
  const pendingFollowupsRef = useRef(new Map());
  // Mirror of `messages`, for the same reason as onWatchTerminalRef below: the
  // 5s tick closure has to read the latest bodies without `messages` being an
  // effect dep, which would tear down and re-arm the poller on every re-render
  // — including the ones our own re-fetch causes.
  const messagesRef = useRef(messages);
  useEffect(() => {
    messagesRef.current = messages;
  }, [messages]);
  // Which conversation pendingFollowupsRef currently describes. The poll effect
  // also re-runs on isProcessing changes, and those must NOT clear deadlines
  // that are still counting down.
  const pendingFollowupsCidRef = useRef(null);
  // Mirror itemProps.onWatchTerminal into a ref so the polling effect's
  // closure can always invoke the latest version without re-creating the
  // poller on every parent re-render. Without this, either (a) we'd put
  // onWatchTerminal in the deps and the 5s poller would tear down/re-up
  // every render, OR (b) we'd be stuck calling a stale callback from the
  // first render.
  const onWatchTerminalRef = useRef(itemProps.onWatchTerminal);
  useEffect(() => {
    onWatchTerminalRef.current = itemProps.onWatchTerminal;
  }, [itemProps.onWatchTerminal]);
  useEffect(() => {
    const cid = itemProps.conversationId;
    // Deadlines are scoped to one conversation — a switch must not carry the
    // previous conversation's pending follow-ups in.
    if (pendingFollowupsCidRef.current !== cid) {
      pendingFollowupsCidRef.current = cid;
      pendingFollowupsRef.current = new Map();
    }
    if (!cid || !watchFeatureEnabled) {
      setWatchCount(0);
      return undefined;
    }
    let aborted = false;
    let timer = null;
    // Track whether the most recent fetch saw a live watch — used by the
    // .catch path so a transient 5xx doesn't permanently kill the poller
    // for the live-watch-on-settled-conversation case (chat is no longer
    // processing but a watch is still polling).
    let lastKnownHasLiveWatch = false;
    // Set on each successful tick so a transient watch-list 5xx cannot strand a
    // follow-up that is still on its way.
    let awaitingFollowup = false;
    // Exponential backoff on consecutive errors (5s → 10s → 20s, cap 30s)
    // so a persistent backend outage doesn't hammer at the steady 5s
    // cadence — and a recovery returns to 5s as soon as one fetch lands.
    let errorBackoffMs = 5000;
    const RESET_BACKOFF_MS = 5000;
    const MAX_BACKOFF_MS = 30000;
    const tick = () => {
      api
        .listWatchesByConversation({ conversationId: cid })
        .then((rows) => {
          if (aborted) {
            return;
          }
          errorBackoffMs = RESET_BACKOFF_MS; // success → reset backoff
          setWatchCount(rows?.length || 0);

          // The responder writes a markdown `Watch update` block into the
          // parent message's `response` text when a watch terminates, but the
          // UI's copy of `messages` is loaded on chat open and never re-fetched
          // on a timer. Without a trigger here the user has to hard-refresh to
          // see it — the chip flips live while the inline body stays stale.
          //
          // Firing once on the status transition is not enough: llm-server
          // commits the terminal status BEFORE the summarizer LLM call that
          // produces the block, so that single re-fetch reads the message back
          // seconds before the block exists, and then the poller stops. Keep
          // re-fetching until the responder's marker actually shows up instead,
          // bounded by WATCH_FOLLOWUP_BUDGET_MS. Keying on the marker rather
          // than on a transition also covers opening a conversation
          // mid-summarize, where the watch is already terminal on first
          // sighting and a transition never fires at all.
          const now = Date.now();
          pendingFollowupsRef.current = reconcilePendingFollowups({
            rows,
            messages: messagesRef.current,
            pending: pendingFollowupsRef.current,
            now,
            budgetMs: WATCH_FOLLOWUP_BUDGET_MS,
          });
          awaitingFollowup = countAwaitingFollowups(pendingFollowupsRef.current, now) > 0;
          if (awaitingFollowup && typeof onWatchTerminalRef.current === 'function') {
            onWatchTerminalRef.current();
          }

          const hasLiveWatch = (rows || []).some((r) => !TERMINAL_WATCH_STATUSES.includes(r.status));
          lastKnownHasLiveWatch = hasLiveWatch;
          // Keep polling as long as the chat is processing or there's still a live watch.
          // A settled chat with all-terminal watches → stop scheduling further fetches.
          if (isProcessing || hasLiveWatch || awaitingFollowup) {
            timer = setTimeout(tick, 5000);
          }
        })
        .catch(() => {
          // Reschedule on transient failure whenever EITHER the chat is
          // still processing OR we last saw a live watch. Previously this
          // only checked isProcessing, which permanently killed the chip
          // poller for live-watch-on-settled-convo on a single 5xx.
          if (aborted) {
            return;
          }
          // Re-read the deadlines rather than reusing the last successful
          // tick's flag: while the endpoint is failing nothing refreshes them,
          // and a stale `true` would keep retrying long past the budget.
          if (isProcessing || lastKnownHasLiveWatch || countAwaitingFollowups(pendingFollowupsRef.current, Date.now()) > 0) {
            timer = setTimeout(tick, errorBackoffMs);
            errorBackoffMs = Math.min(errorBackoffMs * 2, MAX_BACKOFF_MS);
          }
        });
    };
    tick();
    return () => {
      aborted = true;
      if (timer) {
        clearTimeout(timer);
      }
    };
  }, [itemProps.conversationId, isProcessing, watchFeatureEnabled]);

  const handleCardClick = useCallback(
    (index) => {
      setCollapsedObj((prev) => ({ ...prev, [index]: !prev[index] }));
    },
    [setCollapsedObj]
  );

  // Per-question "Show more / Show less" toggle, keyed by message index so each question
  // bubble owns its own expand state. Previously a single shared boolean toggled every
  // question in the conversation at once (issue #35751).
  const handleShowFullText = useCallback(
    (index) => {
      setShowFullText((prev) => ({ ...prev, [index]: !prev[index] }));
    },
    [setShowFullText]
  );

  // Per-task "Tool Details" drawer — used by inline task rows during active runs (no Tasks
  // drawer is open in that case, so we open the primary drawer with the ToolDetails view).
  const handleOpenToolDetails = useCallback(
    (toolCallMessage) => {
      // Load token usage (incl. per-tool reasoning) so the reasoning entry has data.
      itemProps?.handleTokenUsageHover?.();
      setDrawer({ open: true, kind: 'tool-details', title: 'Tool Details', data: { task: toolCallMessage } });
      setSecondary({ open: false, task: null });
    },
    [itemProps?.handleTokenUsageHover]
  );

  const openTasksDrawer = useCallback(
    ({ tasks, expandedTaskKey }) => {
      // Token-usage (incl. reasoning steps) is otherwise fetched only on hover of the
      // usage widget. Trigger it on drawer open so reasoning rows have data; it's
      // guarded to fetch once.
      itemProps?.handleTokenUsageHover?.();
      // Title counts executions; the list keeps every row (the ack is context worth reading).
      setDrawer({ open: true, kind: 'tasks', title: `Tasks · ${actionableTasks(tasks).length}`, data: { tasks } });
      if (expandedTaskKey != null) {
        const target = tasks.find((t) => {
          const candidates = [t.id, t.tool_id, t.originalIndex];
          return candidates.some((c) => c != null && String(c) === String(expandedTaskKey));
        });
        if (target) {
          setSecondary({ open: true, task: target });
          return;
        }
      }
      setSecondary({ open: false, task: null });
    },
    [itemProps?.handleTokenUsageHover]
  );

  const openContextsDrawer = useCallback((references) => {
    setDrawer({ open: true, kind: 'contexts', title: `Additional Contexts · ${references.length}`, data: { references } });
    setSecondary({ open: false, task: null });
  }, []);

  const openMemoriesDrawer = useCallback((memories) => {
    setDrawer({ open: true, kind: 'memories', title: `New Memories · ${memories.length}`, data: { memories } });
    setSecondary({ open: false, task: null });
  }, []);

  const openChannelsDrawer = useCallback((references) => {
    setDrawer({ open: true, kind: 'channels', title: 'Channel Conversation', data: { references } });
    setSecondary({ open: false, task: null });
  }, []);

  // Watches drawer — opens the WatchesTab keyed on the conversation. Unlike
  // tasks/contexts/memories which are per-response artefacts, watches are
  // a conversation-level state, so we render the chip on the most-recent
  // response only (see groupIndex check below) and the drawer always shows
  // every watch in the conversation regardless of which turn registered it.
  const openWatchesDrawer = useCallback(() => {
    setDrawer({
      open: true,
      title: 'Watches',
      content: <WatchesTab conversationId={itemProps.conversationId} />,
    });
  }, [itemProps.conversationId]);

  // Auto-expand newly-arrived followup-question cards in the active group, and scroll the
  // viewport to the latest one so the user notices it. Tracks the count we've already seen
  // per group so we only react to *new* arrivals (polling can return the same set repeatedly).
  const seenFollowupCountRef = useRef({});
  useEffect(() => {
    if (messages.length === 0) {
      seenFollowupCountRef.current = {};
    }
  }, [messages.length]);

  useEffect(() => {
    groupedMessages.forEach((group, groupIndex) => {
      const followups = group.children.filter((c) => (c.tool ?? c.type) === 'followup-question');
      if (followups.length === 0) {
        return;
      }
      const prevCount = seenFollowupCountRef.current[groupIndex] || 0;
      if (followups.length <= prevCount) {
        // Count went down (polling flicker) or stayed the same — sync ref and exit.
        seenFollowupCountRef.current[groupIndex] = followups.length;
        return;
      }
      const newFollowups = followups.slice(prevCount);
      seenFollowupCountRef.current[groupIndex] = followups.length;

      // Auto-expand each newly-arrived followup card.
      setCollapsedObj((prev) => {
        const updates = {};
        newFollowups.forEach((f) => {
          updates[f.originalIndex] = true;
        });
        return { ...prev, ...updates };
      });

      // Scroll to the latest one after the next paint — but only if the bottom-anchored
      // FollowupSheet isn't already taking over for this followup. Otherwise scrolling to
      // a read-only inline card on top would bury the interactive sheet at the bottom.
      const lastFollowup = newFollowups[newFollowups.length - 1];
      if (!lastFollowup) {
        return;
      }
      const lastFollowupKey = `${lastFollowup.response?.message_id || ''}:${lastFollowup.response?.agent_id || ''}`;
      if (itemProps.followupReadOnlyKey && itemProps.followupReadOnlyKey === lastFollowupKey) {
        return;
      }
      requestAnimationFrame(() => {
        const el = document.getElementById(`task-card-${lastFollowup.originalIndex}`);
        if (el) {
          el.scrollIntoView({ behavior: 'smooth', block: 'start' });
        }
      });
    });
  }, [groupedMessages, setCollapsedObj, itemProps.followupReadOnlyKey]);

  // Navigate to a task. If it's still rendered inline (active group, no response yet),
  // expand the card and scroll to it. Otherwise (completed group) open the right drawer
  // with that task pre-expanded.
  const handleNavigateToTask = useCallback(
    (groupIndex, taskOriginalIndex) => {
      const group = groupedMessages[groupIndex];
      if (!group) {
        return;
      }
      const hasResponse = group.children.some((c) => (c.tool ?? c.type) === 'response');
      const tasks = group.children.filter((c) => (c.tool ?? c.type) !== 'question' && (c.tool ?? c.type) !== 'response');
      if (tasks.length === 0) {
        return;
      }

      if (!hasResponse) {
        // Tasks are inline — expand and scroll.
        setCollapsedObj((prev) => ({ ...prev, [taskOriginalIndex]: true }));
        setTimeout(() => {
          const el = document.getElementById(`task-card-${taskOriginalIndex}`);
          if (el) {
            el.scrollIntoView({ behavior: 'smooth', block: 'start' });
          }
        }, 200);
        return;
      }

      // Completed group — open the drawer with the full per-call tree (all agents + tool calls),
      // falling back to the curated tasks if the response predates drawerTasks.
      const response = group.children.find((c) => (c.tool ?? c.type) === 'response');
      const drawerTasks = response?.drawerTasks ?? tasks;
      const target = tasks.find((t) => t.originalIndex === taskOriginalIndex);
      const expandedTaskKey = target?.id || target?.tool_id;
      openTasksDrawer({ tasks: drawerTasks, expandedTaskKey });
    },
    [groupedMessages, openTasksDrawer, setCollapsedObj]
  );

  return (
    <Box>
      {groupedMessages.map((group, groupIndex) => {
        const response = group.children.find((c) => (c.tool ?? c.type) === 'response');
        const tasks = group.children.filter((c) => (c.tool ?? c.type) !== 'question' && (c.tool ?? c.type) !== 'response');
        // The drawer shows the full per-call tree (every agent + tool call); the inline stream and
        // its curated `tasks` are unchanged. Fall back to `tasks` for responses predating drawerTasks.
        const drawerTasks = response?.drawerTasks ?? tasks;
        // Only the number drops conversational rows; the drawer still lists them.
        const taskCount = actionableTasks(drawerTasks).length;
        const extra = response ? additionalData[response.id] : null;
        const allReferences = extra?.references || [];
        // Channel-context provenance gets its own chip + drawer; everything
        // else stays in the Additional Contexts panel as before.
        const references = allReferences.filter((r) => r?.type !== 'channel_context');
        const channelRefs = allReferences.filter((r) => r?.type === 'channel_context');
        const memories = extra?.memories || [];

        const responseTokenData = response ? itemProps.messageTokenData?.[response.id] || itemProps.messageTokenData?.[response.messageId] : null;

        const isLastGroup = groupIndex === groupedMessages.length - 1;
        // Watches are conversation-level state, not per-turn. Surface the
        // count only on the most recent response so the user doesn't see
        // the same "1 watch" chip repeated on every past turn. The drawer
        // still shows every watch in the conversation regardless.
        const watchesForThisGroup = isLastGroup ? watchCount : 0;

        const responseMeta = response
          ? {
              taskCount,
              contextCount: references.length,
              memoryCount: memories.length,
              channelCount: channelRefs.length,
              watchCount: watchesForThisGroup,
              onOpenTasks: taskCount > 0 ? () => openTasksDrawer({ tasks: drawerTasks }) : undefined,
              onOpenContexts: references.length > 0 ? () => openContextsDrawer(references) : undefined,
              onOpenMemories: memories.length > 0 ? () => openMemoriesDrawer(memories) : undefined,
              onOpenChannels: channelRefs.length > 0 ? () => openChannelsDrawer(channelRefs) : undefined,
              onOpenWatches: watchesForThisGroup > 0 ? openWatchesDrawer : undefined,
              messageTokenData: responseTokenData,
              onTokenUsageHover: itemProps.handleTokenUsageHover,
              isFetchingTokenData: itemProps.isFetchingTokenData,
            }
          : null;
        // Inline-render tasks only for groups that haven't produced a response yet.
        // Past turns drop their tasks from the inline view — they remain accessible via the
        // response meta-rail's "Tasks" chip → drawer.
        const showInlineTasks = !response && tasks.length > 0;

        return (
          <React.Fragment key={group.question.originalIndex}>
            <MessageItem
              message={group.question}
              index={group.question.originalIndex}
              isCollapsed={false}
              collapsedObj={collapsedObj}
              onToggle={() => {}}
              showFullText={!!showFullText[group.question.originalIndex]}
              onShowFullText={() => handleShowFullText(group.question.originalIndex)}
              {...itemProps}
            />
            {showInlineTasks &&
              tasks.map((task, taskIdx) => {
                const isLastTaskInGroup = taskIdx === tasks.length - 1;
                return (
                  <MessageItem
                    key={task.originalIndex}
                    message={task}
                    index={task.originalIndex}
                    isLastInGroup={isLastTaskInGroup}
                    isLastTaskOfLastGroup={isLastGroup && isLastTaskInGroup}
                    isCollapsed={!!collapsedObj[task.originalIndex]}
                    collapsedObj={collapsedObj}
                    onToggle={() => handleCardClick(task.originalIndex)}
                    showFullText={!!showFullText[task.originalIndex]}
                    onShowFullText={() => handleShowFullText(task.originalIndex)}
                    isLoadingInvestigation={isProcessing}
                    {...itemProps}
                    generateQuestionText={group?.question?.text || itemProps?.generateQuestionText}
                    siblingTasks={tasks}
                    agentTokenData={itemProps.getAgentTokenDataForMessage?.(task)}
                    messageTokenData={itemProps.messageTokenData?.[task.id] || itemProps.messageTokenData?.[task.messageId]}
                    onOpenToolDetails={handleOpenToolDetails}
                    onNavigateToTask={handleNavigateToTask}
                    groupIndex={groupIndex}
                  />
                );
              })}
            {response && (
              <MessageItem
                key={response.originalIndex}
                message={response}
                index={response.originalIndex}
                isLastInGroup={true}
                isLastTaskOfLastGroup={isLastGroup}
                isCollapsed={!!collapsedObj[response.originalIndex]}
                collapsedObj={collapsedObj}
                onToggle={() => handleCardClick(response.originalIndex)}
                showFullText={!!showFullText[response.originalIndex]}
                onShowFullText={() => handleShowFullText(response.originalIndex)}
                isLoadingInvestigation={isProcessing}
                {...itemProps}
                generateQuestionText={group?.question?.text || itemProps?.generateQuestionText}
                siblingTasks={tasks}
                agentTokenData={itemProps.getAgentTokenDataForMessage(response)}
                messageTokenData={itemProps.messageTokenData?.[response.id] || itemProps.messageTokenData?.[response.messageId]}
                onNavigateToTask={handleNavigateToTask}
                groupIndex={groupIndex}
                responseMeta={responseMeta}
                feedback={additionalData[response.id]?.feedback}
                feedbackManagedExternally
              />
            )}
          </React.Fragment>
        );
      })}

      <CustomDrawer
        open={drawer.open}
        onClose={closeDrawer}
        title={drawer.title}
        width='38%'
        onWidthChange={setPrimaryWidth}
        resizable={false}
        variant={drawer.kind === 'tasks' ? 'modern' : 'default'}
      >
        <Box sx={{ color: 'var(--ds-gray-700)' }}>
          {renderDrawerContent({
            drawer,
            secondary,
            itemProps,
            onOpenToolDetails: openSecondaryToolDetails,
          })}
        </Box>
      </CustomDrawer>

      <SecondaryDrawer
        open={secondary.open && drawer.open && drawer.kind === 'tasks'}
        onClose={closeSecondary}
        title='Tool Details'
        rightOffset={primaryWidth}
        defaultWidth='45%'
        variant='modern'
      >
        {secondary.task && (
          <ToolDetails
            toolCall={secondary.task}
            accountId={itemProps.accountId}
            conversationId={itemProps.conversationId}
            getReasoningForTool={itemProps?.getReasoningForTool}
          />
        )}
      </SecondaryDrawer>
    </Box>
  );
};

const renderDrawerContent = ({ drawer, secondary, itemProps, onOpenToolDetails }) => {
  // Two drawer-population styles are in use:
  //   (1) data-driven: openTasksDrawer / openContextsDrawer / openMemoriesDrawer
  //       set { kind, data } and the switch below renders the right component
  //       wiring `data` in as props.
  //   (2) content-driven: openWatchesDrawer sets { content: <WatchesTab /> }
  //       and the JSX renders as-is. Honour this first — the WatchesTab fetches
  //       its own data via api.listWatchesByConversation so it needs no `data`
  //       prop, and the `kind` switch has no entry for it. Returning null when
  //       `kind` was unset (as the previous guard did) made the watches drawer
  //       appear empty even though the chip count was right.
  if (drawer.content) {
    return drawer.content;
  }
  if (!drawer.kind || !drawer.data) {
    return null;
  }
  switch (drawer.kind) {
    case 'tasks':
      return (
        <TasksDrawerContent
          tasks={drawer.data.tasks}
          accountId={itemProps.accountId}
          conversationId={itemProps.conversationId}
          activeTaskKey={taskKey(secondary.task)}
          onOpenToolDetails={onOpenToolDetails}
          itemProps={itemProps}
        />
      );
    case 'contexts':
      // Two-level filter (category tabs → subtype pills → filtered table)
      // lives in ReferencesDrawerContent so the same UX renders on both
      // this drawer and the LLMConversationWithTabs Additional Contexts tab.
      return <ReferencesDrawerContent references={drawer.data.references} />;
    case 'channels':
      return <ChannelContextDrawerContent references={drawer.data.references} />;
    case 'memories':
      return <MemoriesDrawerContent memories={drawer.data.memories} />;
    case 'tool-details':
      return (
        <ToolDetails
          toolCall={drawer.data.task}
          accountId={itemProps.accountId}
          conversationId={itemProps.conversationId}
          getReasoningForTool={itemProps?.getReasoningForTool}
        />
      );
    default:
      return null;
  }
};

MessageStream.propTypes = {
  messages: PropTypes.array.isRequired,
  isProcessing: PropTypes.bool,
  collapsedObj: PropTypes.object,
  setCollapsedObj: PropTypes.func.isRequired,
  showFullText: PropTypes.object,
  setShowFullText: PropTypes.func.isRequired,
  itemProps: PropTypes.object.isRequired,
};

export default MessageStream;
