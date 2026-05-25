import React, { useState, useEffect, useCallback, useRef } from 'react';
import { Box, Typography, Skeleton, Divider } from '@mui/material';
import PendingActionsOutlinedIcon from '@mui/icons-material/PendingActionsOutlined';
import Link from 'next/link';
import PropTypes from 'prop-types';
import dayjs from 'dayjs';
import relativeTime from 'dayjs/plugin/relativeTime';
import Card from '@components1/ds/Card';
import { Chip } from '@components1/ds/Chip';
import { Button } from '@components1/ds/Button';
import Tooltip from '@components1/ds/Tooltip';
import ChatBubbleOutlineIcon from '@mui/icons-material/ChatBubbleOutline';
import KeyboardArrowRightIcon from '@mui/icons-material/KeyboardArrowRight';
import ToggleButtons from '@components1/workflow/NewToggleButtons';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { getUserSession } from '@lib/auth';
import { useTenantBranding } from '@hooks/useTenantBranding';
import { UserIconOutline, UsersIcon } from '@assets';

dayjs.extend(relativeTime);

const SOURCE_CONFIG = {
  PrometheusQuery: { label: 'Prometheus', color: 'var(--ds-red-600)', bg: 'var(--ds-red-100)' },
  LokiQuery: { label: 'Loki', color: 'var(--ds-purple-600)', bg: 'var(--ds-purple-100)' },
  ESQuery: { label: 'ES Query', color: 'var(--ds-teal-600)', bg: 'var(--ds-teal-100)' },
  Investigation: { label: 'Event', color: 'var(--ds-amber-600)', bg: 'var(--ds-amber-100)' },
  UserInvestigation: { label: 'User Chat', color: 'var(--ds-blue-600)', bg: 'var(--ds-blue-100)' },
  InstantNotification: { label: 'Slack', color: 'var(--ds-green-600)', bg: 'var(--ds-green-100)' },
};

const MAX_DISPLAY = 2;
const FETCH_LIMIT = 5;

const TruncatableTitle = ({ text, sx }) => {
  const measureRef = useRef(null);
  const [truncated, setTruncated] = useState(false);

  useEffect(() => {
    const el = measureRef.current;
    if (!el) return undefined;
    const check = () => {
      setTruncated(el.scrollWidth > el.clientWidth);
    };
    const raf = requestAnimationFrame(check);
    const ro = new ResizeObserver(check);
    ro.observe(el);
    return () => {
      cancelAnimationFrame(raf);
      ro.disconnect();
    };
  }, [text]);

  // CustomTooltip short-circuits to <>{children}</> when title is empty, which
  // skips its cloneElement step — meaning a Tooltip-forwarded ref never
  // attaches to the child. So we always pass a non-empty title and gate
  // visibility via disableHoverListener. The Tooltip forwards our measureRef
  // through cloneElement to the underlying Typography DOM element.
  return (
    <Tooltip
      ref={measureRef}
      title={text}
      placement='top'
      disableHoverListener={!truncated}
      disableFocusListener={!truncated}
      disableTouchListener={!truncated}
    >
      <Typography sx={{ ...sx, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{text}</Typography>
    </Tooltip>
  );
};
TruncatableTitle.propTypes = {
  text: PropTypes.string.isRequired,
  sx: PropTypes.object,
};

const PendingFollowUps = ({ accountId }) => {
  const { assistantName } = useTenantBranding();
  const latestRequestRef = useRef(0);
  const [activeTab, setActiveTab] = useState('yours');
  const [yourConversations, setYourConversations] = useState([]);
  const [teamConversations, setTeamConversations] = useState([]);
  const [yourCount, setYourCount] = useState(0);
  const [teamCount, setTeamCount] = useState(0);
  const [loading, setLoading] = useState(false);

  const fetchConversations = useCallback(async () => {
    if (!accountId) {
      setYourConversations([]);
      setTeamConversations([]);
      setYourCount(0);
      setTeamCount(0);
      setLoading(false);
      return;
    }
    const requestId = ++latestRequestRef.current;
    setLoading(true);
    try {
      const userEmail = getUserSession()?.user?.email;

      const [yoursResponse, teamResponse] = await Promise.all([
        apiAskNudgebee.llmConversationHistory({
          account_id: accountId,
          status: 'WAITING',
          user_username: userEmail,
          limit: FETCH_LIMIT,
          offset: 0,
        }),
        apiAskNudgebee.llmConversationHistory({
          account_id: accountId,
          status: 'WAITING',
          user_username_neq: userEmail,
          limit: FETCH_LIMIT,
          offset: 0,
        }),
      ]);

      if (requestId !== latestRequestRef.current) return;

      const yoursItems = yoursResponse?.data?.data?.llm_conversations || [];
      const yoursTotal = yoursResponse?.data?.data?.llm_conversations_aggregate?.aggregate?.count || 0;
      const teamItems = teamResponse?.data?.data?.llm_conversations || [];
      const teamTotal = teamResponse?.data?.data?.llm_conversations_aggregate?.aggregate?.count || 0;

      setYourConversations(yoursItems);
      setTeamConversations(teamItems);
      setYourCount(yoursTotal);
      setTeamCount(teamTotal);
    } catch (error) {
      console.error('Failed to fetch pending follow-ups:', error);
      if (requestId === latestRequestRef.current) {
        setYourConversations([]);
        setTeamConversations([]);
        setYourCount(0);
        setTeamCount(0);
      }
    } finally {
      if (requestId === latestRequestRef.current) {
        setLoading(false);
      }
    }
  }, [accountId]);

  useEffect(() => {
    fetchConversations();
  }, [fetchConversations]);

  useEffect(() => {
    if (loading) return;
    if (activeTab === 'yours' && yourCount === 0 && teamCount > 0) {
      setActiveTab('team');
    } else if (activeTab === 'team' && teamCount === 0 && yourCount > 0) {
      setActiveTab('yours');
    }
  }, [loading, activeTab, yourCount, teamCount]);

  const getRelativeTime = (dateStr) => {
    if (!dateStr) {
      return '';
    }
    return dayjs(dateStr).fromNow();
  };

  const conversations = activeTab === 'yours' ? yourConversations : teamConversations;
  const totalCount = activeTab === 'yours' ? yourCount : teamCount;
  const displayItems = conversations.slice(0, MAX_DISPLAY);

  const toggleOptions = [
    { value: 'yours', label: `Yours  ${yourCount}`, icon: UserIconOutline, disabled: yourCount === 0 },
    { value: 'team', label: `Team  ${teamCount}`, icon: UsersIcon, disabled: teamCount === 0 },
  ];

  if (!loading && yourCount === 0 && teamCount === 0) {
    return null;
  }

  const viewAllHref = `/ask-nudgebee?accountId=${accountId}&status=WAITING${activeTab === 'yours' ? '&filter=Mine' : ''}`;

  const header = (
    <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-3)' }}>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', minWidth: 0 }}>
        <Box
          sx={{
            width: 28,
            height: 28,
            borderRadius: 'var(--ds-radius-md)',
            backgroundColor: 'var(--ds-amber-100)',
            color: 'var(--ds-amber-600)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            flexShrink: 0,
          }}
        >
          <PendingActionsOutlinedIcon sx={{ fontSize: 16 }} />
        </Box>
        <Typography
          sx={{
            fontFamily: 'var(--ds-font-sans)',
            fontSize: 'var(--ds-text-body)',
            fontWeight: 600,
            color: 'var(--ds-gray-700)',
            letterSpacing: '-0.01em',
          }}
        >{`${assistantName} Follow-ups`}</Typography>
      </Box>
      {totalCount > MAX_DISPLAY && (
        <Button
          tone='ghost'
          size='xs'
          icon={<KeyboardArrowRightIcon />}
          iconPlacement='end'
          onClick={() => window.open(viewAllHref, '_blank', 'noopener,noreferrer')}
          data-testid='follow-ups-view-all'
        >
          View all
        </Button>
      )}
    </Box>
  );

  return (
    <Card size='sm' elevation='flat' header={header} sx={{ minHeight: '90px', overflow: 'hidden' }}>
      <Box sx={{ mb: '8px' }}>
        <ToggleButtons options={toggleOptions} activeValue={activeTab} onChange={setActiveTab} size='sm' noShadow width='100%' />
      </Box>

      {loading && (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
          {[1, 2, 3].map((i) => (
            <Box key={i} sx={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
              <Skeleton variant='text' width='80%' height={16} />
              <Skeleton variant='text' width='40%' height={12} />
            </Box>
          ))}
        </Box>
      )}
      {!loading && displayItems.length === 0 && (
        <Box sx={{ py: '12px', textAlign: 'center' }}>
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-400)' }}>No pending follow-ups</Typography>
        </Box>
      )}
      {!loading && displayItems.length > 0 && (
        <Box sx={{ display: 'flex', flexDirection: 'column' }}>
          {displayItems.map((conv, index) => {
            const srcCfg = conv.source && SOURCE_CONFIG[conv.source];
            return (
              <React.Fragment key={conv.id}>
                <Link
                  href={`/ask-nudgebee?accountId=${accountId}&session_id=${conv.session_id}&status=WAITING`}
                  target='_blank'
                  rel='noopener noreferrer'
                  style={{ textDecoration: 'none' }}
                >
                  <Box
                    data-testid={`follow-up-item-${index}`}
                    sx={{
                      display: 'flex',
                      alignItems: 'flex-start',
                      gap: 'var(--ds-space-3)',
                      py: 'var(--ds-space-2)',
                      px: 'var(--ds-space-2)',
                      cursor: 'pointer',
                      borderRadius: 'var(--ds-radius-sm)',
                    }}
                  >
                    <Box
                      sx={{
                        width: 26,
                        height: 26,
                        borderRadius: '50%',
                        backgroundColor: 'var(--ds-yellow-100)',
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        flexShrink: 0,
                        mt: '1px',
                      }}
                    >
                      <ChatBubbleOutlineIcon sx={{ fontSize: 12, color: 'var(--ds-brand-600)' }} />
                    </Box>
                    <Box sx={{ flex: 1, minWidth: 0 }}>
                      <TruncatableTitle
                        text={conv.title || 'Untitled conversation'}
                        sx={{
                          fontSize: 'var(--ds-text-small)',
                          fontWeight: 400,
                          color: 'var(--ds-gray-700)',
                          lineHeight: 1.4,
                        }}
                      />
                      <Box sx={{ display: 'flex', alignItems: 'baseline', gap: '6px', mt: '4px', flexWrap: 'wrap' }}>
                        {srcCfg && (
                          <Chip
                            variant='tag'
                            size='xs'
                            shape='rect'
                            sx={{
                              color: srcCfg.color,
                              backgroundColor: srcCfg.bg,
                              border: 'none',
                              fontWeight: 500,
                            }}
                          >
                            {srcCfg.label}
                          </Chip>
                        )}
                        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-400)', mt: '2px' }}>
                          {getRelativeTime(conv.updated_at)}
                        </Typography>
                      </Box>
                    </Box>
                  </Box>
                </Link>
                {index < displayItems.length - 1 && <Divider sx={{ borderColor: 'var(--ds-gray-100)', mx: 'var(--ds-space-2)' }} />}
              </React.Fragment>
            );
          })}
        </Box>
      )}
    </Card>
  );
};

PendingFollowUps.propTypes = {
  accountId: PropTypes.string,
};

export default PendingFollowUps;
