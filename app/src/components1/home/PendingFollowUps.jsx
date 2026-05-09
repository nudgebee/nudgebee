import React, { useState, useEffect, useCallback, useRef } from 'react';
import { Box, Typography, Skeleton, Divider } from '@mui/material';
import PendingActionsOutlinedIcon from '@mui/icons-material/PendingActionsOutlined';
import Link from 'next/link';
import PropTypes from 'prop-types';
import dayjs from 'dayjs';
import relativeTime from 'dayjs/plugin/relativeTime';
import WidgetCard from '@components1/common/WidgetCard';
import ToggleButtons from '@components1/workflow/NewToggleButtons';
import { colors } from 'src/utils/colors';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { getUserSession } from '@lib/auth';
import { useTenantBranding } from '@hooks/useTenantBranding';
import { UserIconOutline, UsersIcon } from '@assets';

dayjs.extend(relativeTime);

const SOURCE_CONFIG = {
  PrometheusQuery: { label: 'Prometheus', color: '#DC2626', bg: '#FEF2F2' },
  LokiQuery: { label: 'Loki', color: '#7C3AED', bg: '#F5F3FF' },
  ESQuery: { label: 'ES Query', color: '#0369A1', bg: '#F0F9FF' },
  Investigation: { label: 'Event', color: '#EA580C', bg: '#FFF7ED' },
  UserInvestigation: { label: 'User Chat', color: '#2563EB', bg: '#EFF6FF' },
  InstantNotification: { label: 'Slack', color: '#059669', bg: '#ECFDF5' },
};

const MAX_DISPLAY = 3;
const FETCH_LIMIT = 5;

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

  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: '8px' }}>
        <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
          <PendingActionsOutlinedIcon sx={{ fontSize: '20px', color: colors.border.warning }} />
          <Typography sx={{ fontSize: '13px', fontWeight: 600, color: colors.text.secondary }}>{`${assistantName} Follow-ups`}</Typography>
        </Box>
      </Box>
      <WidgetCard sx={{ mt: '0px', padding: '12px 16px', minHeight: '90px', overflow: 'hidden', boxShadow: 'none' }}>
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
            <Typography sx={{ fontSize: '12px', color: colors.text.secondaryDark }}>No pending follow-ups</Typography>
          </Box>
        )}
        {!loading && displayItems.length > 0 && (
          <Box sx={{ display: 'flex', flexDirection: 'column' }}>
            {displayItems.map((conv, index) => (
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
                      gap: '10px',
                      py: '8px',
                      px: '10px',
                      cursor: 'pointer',
                      '&:hover': {
                        backgroundColor: colors.background.primaryLightest,
                      },
                      borderRadius: '6px',
                    }}
                  >
                    <Box sx={{ flex: 1, minWidth: 0 }}>
                      <Typography
                        sx={{
                          fontSize: '12px',
                          fontWeight: 400,
                          color: colors.text.secondary,
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                        }}
                      >
                        {conv.title || 'Untitled conversation'}
                      </Typography>
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px', mt: '2px' }}>
                        {activeTab === 'team' && conv.source && SOURCE_CONFIG[conv.source] && (
                          <Box
                            sx={{
                              display: 'inline-block',
                              px: '6px',
                              py: '1px',
                              borderRadius: '4px',
                              fontSize: '10px',
                              fontWeight: 500,
                              backgroundColor: SOURCE_CONFIG[conv.source].bg,
                              color: SOURCE_CONFIG[conv.source].color,
                              whiteSpace: 'nowrap',
                            }}
                          >
                            {SOURCE_CONFIG[conv.source].label}
                          </Box>
                        )}
                        <Typography sx={{ fontSize: '11px', color: colors.text.secondaryDark }}>{getRelativeTime(conv.updated_at)}</Typography>
                      </Box>
                    </Box>
                  </Box>
                </Link>
                {index < displayItems.length - 1 && <Divider sx={{ borderColor: colors.border.tertiaryLightest, mx: '4px' }} />}
              </React.Fragment>
            ))}
          </Box>
        )}

        {totalCount > MAX_DISPLAY && (
          <Link
            href={`/ask-nudgebee?accountId=${accountId}&status=WAITING${activeTab === 'yours' ? '&filter=Mine' : ''}`}
            target='_blank'
            rel='noopener noreferrer'
            style={{ textDecoration: 'none' }}
            data-testid='follow-ups-view-all'
          >
            <Typography
              sx={{
                fontSize: '11px',
                color: colors.text.primary,
                pt: '6px',
                cursor: 'pointer',
                '&:hover': { opacity: 0.8 },
              }}
            >
              View all {totalCount} follow ups &rarr;
            </Typography>
          </Link>
        )}
      </WidgetCard>
    </Box>
  );
};

PendingFollowUps.propTypes = {
  accountId: PropTypes.string,
};

export default PendingFollowUps;
