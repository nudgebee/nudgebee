import React, { useState, useEffect, useMemo, useCallback } from 'react';
import { Box, Typography, Grid, Button, Tooltip } from '@mui/material';
import { Input } from '@components1/ds/Input';
import OpenInNewIcon from '@mui/icons-material/OpenInNew';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomSearch from '@common-new/CustomSearch';
import DownloadButton from '@common-new/DownloadButton';
import CustomTable2 from '@common-new/tables/CustomTable2';
import CloudProviderIcon from '@components1/common/CloudIcon';
import CopyButton from '@common-new/CopyButton';
import Datetime from '@common-new/format/Datetime';
import Text from '@common-new/format/Text';
import ticketsApi from '@api1/tickets';
import { Label } from '@components1/ds/Label';
import { SeverityIcon } from '@components1/ds/SeverityIcon';
import PropTypes from 'prop-types';
import apiAccount from '@api1/account';
import { toast as snackbar } from '@components1/ds/Toast';
import { ds } from '@utils/colors';

const TICKET_HEADERS = [
  { name: 'Ticket ID', width: '10%' },
  { name: 'Tool', width: '5%' },
  { name: 'Title', width: '20%' },
  { name: 'Priority', width: '8%' },
  { name: 'Status', width: '9%' },
  { name: 'Account', width: '10%' },
  { name: 'Created By', width: '10%' },
  { name: 'Assignee', width: '12%' },
  { name: 'Created At', sortEnabled: true, width: '10%' },
];

const TOOL_DISPLAY_NAMES = {
  jira: 'Jira',
  github: 'GitHub',
  gitlab: 'GitLab',
  servicenow: 'ServiceNow',
  pagerduty: 'PagerDuty',
  zenduty: 'ZenDuty',
};

const customPriorityOrder = ['Highest', 'High', 'Medium', 'Low', 'Lowest', 'NA', 'Critical'];

// Ticket priorities arrive as 'Critical' / 'Highest' / 'High' / 'Medium' /
// 'Low' / 'Lowest' / 'NA' from the API. ds/SeverityIcon's `level` enum is
// the 5-tier 'critical' | 'high' | 'medium' | 'low' | 'info'. Highest folds
// to critical, Lowest/NA to info; case-normalize for direct hits.
const PRIORITY_TO_DS_LEVEL = {
  critical: 'critical',
  highest: 'critical',
  high: 'high',
  medium: 'medium',
  low: 'low',
  lowest: 'info',
  na: 'info',
};

const toDsSeverityLevel = (priority) => PRIORITY_TO_DS_LEVEL[String(priority || '').toLowerCase()] || 'info';

const TicketDetailsComponent = ({ ticketData, accountsData }) => {
  const isJiraTicket = ticketData?.platform?.toLowerCase() === 'jira';
  const [newComment, setNewComment] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [comments, setComments] = useState([]);
  const [loadingComments, setLoadingComments] = useState(false);
  const [commentFocused, setCommentFocused] = useState(false);

  const parseDescription = (description) => {
    if (!description) {
      return [];
    }

    const regex = /\*\*([^*]+)\*\*:\s*(.*)/g;
    return Array.from(description.matchAll(regex), (match) => ({
      key: match[1].trim(),
      value: match[2].trim(),
    }));
  };

  const fetchComments = async () => {
    if (!isJiraTicket || !ticketData?.ticket_id) {
      return;
    }

    const accountId = ticketData.account_id;
    const configId = ticketData.integration_id;

    setLoadingComments(true);
    try {
      const response = await ticketsApi.getTicketComments(accountId, configId, 'jira', ticketData.ticket_id);

      if (response?.data?.error) {
        setComments([]);
      } else {
        setComments(response?.data?.comments || []);
      }
    } catch {
      setComments([]);
    } finally {
      setLoadingComments(false);
    }
  };

  const handleCommentSubmit = async () => {
    if (!newComment.trim()) {
      return;
    }

    setIsSubmitting(true);
    try {
      const accountId = ticketData.account_id;
      const response = await ticketsApi.addTicketComment(accountId, ticketData.integration_id, 'jira', ticketData.ticket_id, newComment.trim());

      if (response?.data?.success) {
        setNewComment('');
        if (response?.data?.comments) {
          setComments(response.data.comments);
        } else {
          await fetchComments();
        }
        snackbar.success('Comment added successfully!');
      } else {
        snackbar.error(response?.data?.error || 'Failed to add comment');
      }
    } catch {
      snackbar.error('Error adding comment. Please try again.');
    } finally {
      setIsSubmitting(false);
    }
  };

  // Get account name from pre-loaded accounts data
  const getAccountName = () => {
    if (!ticketData?.account_id || !accountsData) {
      return null;
    }
    const account = accountsData.find((acc) => acc.id === ticketData.account_id);
    return account?.account_name || null;
  };

  useEffect(() => {
    if (isJiraTicket && ticketData?.ticket_id) {
      setComments([]); // Clear previous comments
      setLoadingComments(false); // Reset loading state
      fetchComments();
    } else {
      setComments([]); // Clear comments for non-Jira tickets
    }
  }, [ticketData?.ticket_id, ticketData?.account_id, isJiraTicket]);

  const toolDisplayName = TOOL_DISPLAY_NAMES[ticketData?.platform?.toLowerCase()] || ticketData?.platform || 'External Tool';

  return (
    <Box sx={{ p: 'var(--ds-space-4)', backgroundColor: 'var(--ds-background-100)', borderRadius: ds.radius.xl }}>
      {/* Quick action: Open in external tool */}
      {ticketData?.url && (
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', mb: 'var(--ds-space-2)' }}>
          <Button
            variant='outlined'
            size='small'
            startIcon={<OpenInNewIcon sx={{ fontSize: 'var(--ds-text-title)' }} />}
            href={ticketData.url}
            target='_blank'
            data-testid='open-in-tool-btn'
            sx={{
              textTransform: 'none',
              fontWeight: 'var(--ds-font-weight-medium)',
              fontSize: 'var(--ds-text-small)',
              borderRadius: 'var(--ds-radius-md)',
              borderColor: 'var(--ds-gray-300)',
              color: 'var(--ds-gray-700)',
              '&:hover': {
                borderColor: 'var(--ds-blue-500)',
                backgroundColor: 'var(--ds-gray-100)',
              },
            }}
          >
            Open in {toolDisplayName}
          </Button>
        </Box>
      )}

      <Grid container spacing={2}>
        {/* Description */}
        <Grid item xs={12} md={6}>
          <Typography variant='subtitle2' sx={{ fontWeight: 'bold', mb: ds.space[2] }}>
            Description
          </Typography>
          {(() => {
            const parsedData = parseDescription(ticketData?.description);
            if (parsedData.length > 0) {
              return (
                <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
                  {parsedData.map((item, index) => (
                    <Typography key={index} variant='body2'>
                      <strong>{item.key}:</strong> {item.value}
                    </Typography>
                  ))}
                </Box>
              );
            }
            return (
              <Typography variant='body2' sx={{ mb: ds.space[4] }}>
                {ticketData?.description || 'No description available'}
              </Typography>
            );
          })()}
        </Grid>

        {/* Additional Details - structured card layout */}
        <Grid item xs={12} md={6}>
          <Typography variant='subtitle2' sx={{ fontWeight: 'bold', mb: ds.space[2] }}>
            Additional Details
          </Typography>
          <Box
            sx={{
              display: 'grid',
              gridTemplateColumns: '130px 1fr',
              gap: 'var(--ds-space-1) var(--ds-space-3)',
              p: 'var(--ds-space-3)',
              backgroundColor: 'var(--ds-gray-100)',
              borderRadius: 'var(--ds-radius-md)',
            }}
          >
            {ticketData?.ticket_type && (
              <>
                <Typography
                  variant='body2'
                  sx={{ color: 'var(--ds-gray-600)', fontWeight: 'var(--ds-font-weight-medium)', fontSize: 'var(--ds-text-small)' }}
                >
                  Ticket Type
                </Typography>
                <Typography variant='body2' sx={{ fontSize: 'var(--ds-text-small)' }}>
                  {ticketData.ticket_type}
                </Typography>
              </>
            )}
            {ticketData?.source && (
              <>
                <Typography
                  variant='body2'
                  sx={{ color: 'var(--ds-gray-600)', fontWeight: 'var(--ds-font-weight-medium)', fontSize: 'var(--ds-text-small)' }}
                >
                  Source
                </Typography>
                <Typography variant='body2' sx={{ fontSize: 'var(--ds-text-small)' }}>
                  {ticketData.source}
                </Typography>
              </>
            )}
            {ticketData?.reference_id && (
              <>
                <Typography
                  variant='body2'
                  sx={{ color: 'var(--ds-gray-600)', fontWeight: 'var(--ds-font-weight-medium)', fontSize: 'var(--ds-text-small)' }}
                >
                  Reference ID
                </Typography>
                <Typography variant='body2' sx={{ fontSize: 'var(--ds-text-small)' }}>
                  {ticketData.reference_id}
                </Typography>
              </>
            )}
            {ticketData?.updated_at && (
              <>
                <Typography
                  variant='body2'
                  sx={{ color: 'var(--ds-gray-600)', fontWeight: 'var(--ds-font-weight-medium)', fontSize: 'var(--ds-text-small)' }}
                >
                  Last Updated
                </Typography>
                <Typography variant='body2' sx={{ fontSize: 'var(--ds-text-small)' }}>
                  <Datetime value={ticketData.updated_at} />
                </Typography>
              </>
            )}
            {ticketData?.project_key && (
              <>
                <Typography
                  variant='body2'
                  sx={{ color: 'var(--ds-gray-600)', fontWeight: 'var(--ds-font-weight-medium)', fontSize: 'var(--ds-text-small)' }}
                >
                  Project Key
                </Typography>
                <Typography variant='body2' sx={{ fontSize: 'var(--ds-text-small)' }}>
                  {ticketData.project_key}
                </Typography>
              </>
            )}
            {ticketData?.account_id && (
              <>
                <Typography
                  variant='body2'
                  sx={{ color: 'var(--ds-gray-600)', fontWeight: 'var(--ds-font-weight-medium)', fontSize: 'var(--ds-text-small)' }}
                >
                  Account
                </Typography>
                <Typography variant='body2' sx={{ fontSize: 'var(--ds-text-small)' }}>
                  {getAccountName() || ticketData.account_id}
                </Typography>
              </>
            )}
          </Box>
        </Grid>

        {/* Jira Comments Section */}
        {isJiraTicket && (
          <Grid item xs={12}>
            <Typography
              variant='subtitle2'
              sx={{ fontWeight: 'var(--ds-font-weight-semibold)', mb: 'var(--ds-space-3)', color: 'var(--ds-gray-700)' }}
            >
              Comments
            </Typography>
            <Box
              sx={{
                border: '1px solid var(--ds-gray-300)',
                borderRadius: 'var(--ds-radius-md)',
                backgroundColor: 'var(--ds-background-100)',
                boxShadow: '0px 2px 8px rgba(0, 0, 0, 0.04)',
                overflow: 'hidden',
              }}
            >
              {/* Existing Comments Display */}
              <Box
                sx={{
                  p: 'var(--ds-space-4)',
                  maxHeight: ds.space.mul(0, 175),
                  overflowY: 'auto',
                  backgroundColor: 'var(--ds-gray-100)',
                  '&::-webkit-scrollbar': {
                    width: ds.space.mul(0, 3),
                  },
                  '&::-webkit-scrollbar-track': {
                    backgroundColor: 'var(--ds-gray-100)',
                  },
                  '&::-webkit-scrollbar-thumb': {
                    backgroundColor: 'var(--ds-gray-300)',
                    borderRadius: 'var(--ds-radius-sm)',
                  },
                }}
              >
                {loadingComments ? (
                  <Typography variant='body2' sx={{ color: 'var(--ds-gray-600)', fontStyle: 'italic' }}>
                    Loading comments...
                  </Typography>
                ) : comments.length > 0 ? (
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0 }}>
                    {comments.map((comment, index) => (
                      <Box
                        key={index}
                        sx={{
                          py: 'var(--ds-space-3)',
                          px: 'var(--ds-space-2)',
                          borderBottom: index < comments.length - 1 ? '1px solid var(--ds-gray-300)' : 'none',
                          transition: 'background-color 0.2s ease',
                          '&:hover': {
                            backgroundColor: 'var(--ds-background-100)',
                          },
                        }}
                      >
                        <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--ds-space-2)' }}>
                          <Box
                            sx={{
                              minWidth: ds.space.mul(1, 7),
                              width: ds.space.mul(1, 7),
                              height: ds.space.mul(1, 7),
                              borderRadius: ds.radius.pill,
                              backgroundColor: 'var(--ds-blue-100)',
                              display: 'flex',
                              alignItems: 'center',
                              justifyContent: 'center',
                              fontWeight: 'var(--ds-font-weight-semibold)',
                              fontSize: 'var(--ds-text-small)',
                              color: 'var(--ds-blue-500)',
                              flexShrink: 0,
                            }}
                          >
                            {(comment.author || 'U').charAt(0).toUpperCase()}
                          </Box>
                          <Box sx={{ flex: 1, minWidth: 0 }}>
                            <Box
                              sx={{ display: 'flex', alignItems: 'baseline', gap: 'var(--ds-space-2)', mb: 'var(--ds-space-1)', flexWrap: 'wrap' }}
                            >
                              <Typography
                                variant='body2'
                                sx={{ fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-700)', fontSize: 'var(--ds-text-body)' }}
                              >
                                {comment.author || 'Unknown'}
                              </Typography>
                              <Typography variant='caption' sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-caption)' }}>
                                {comment.created_at ? <Datetime value={comment.created_at} /> : 'Unknown date'}
                              </Typography>
                              {comment.updated_at && comment.updated_at !== comment.created_at && (
                                <Typography
                                  variant='caption'
                                  sx={{ color: 'var(--ds-gray-600)', fontStyle: 'italic', fontSize: 'var(--ds-text-caption)' }}
                                >
                                  • edited
                                </Typography>
                              )}
                            </Box>
                            <Typography
                              variant='body2'
                              sx={{
                                whiteSpace: 'pre-wrap',
                                color: 'var(--ds-gray-700)',
                                lineHeight: 1.5,
                                fontSize: 'var(--ds-text-body)',
                              }}
                            >
                              {comment.comment || 'No content'}
                            </Typography>
                          </Box>
                        </Box>
                      </Box>
                    ))}
                  </Box>
                ) : (
                  <Box sx={{ textAlign: 'center', py: 'var(--ds-space-5)', px: 'var(--ds-space-4)' }}>
                    <Typography variant='body2' sx={{ color: 'var(--ds-gray-600)', fontStyle: 'italic' }}>
                      No comments yet. Be the first to add a comment!
                    </Typography>
                  </Box>
                )}
              </Box>

              {/* Add New Comment - compact input */}
              <Box
                sx={{
                  p: 'var(--ds-space-4)',
                  backgroundColor: 'var(--ds-background-100)',
                  borderTop: '1px solid var(--ds-gray-300)',
                }}
              >
                <Box sx={{ mb: commentFocused || newComment ? 'var(--ds-space-4)' : 0 }}>
                  <Input
                    type='textarea'
                    rows={commentFocused || newComment ? 3 : 1}
                    placeholder='Write a comment...'
                    value={newComment}
                    onChange={setNewComment}
                    onFocus={() => setCommentFocused(true)}
                    onBlur={() => {
                      if (!newComment) setCommentFocused(false);
                    }}
                    size='sm'
                  />
                </Box>
                {(commentFocused || newComment) && (
                  <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--ds-space-3)' }}>
                    <Button
                      variant='outlined'
                      size='small'
                      onClick={() => {
                        setNewComment('');
                        setCommentFocused(false);
                      }}
                      disabled={isSubmitting}
                      sx={{
                        borderRadius: 'var(--ds-radius-md)',
                        textTransform: 'none',
                        fontWeight: 'var(--ds-font-weight-medium)',
                        borderColor: 'var(--ds-gray-300)',
                        color: 'var(--ds-gray-600)',
                        '&:hover': {
                          borderColor: 'var(--ds-blue-500)',
                          backgroundColor: 'var(--ds-gray-100)',
                        },
                      }}
                    >
                      Cancel
                    </Button>
                    <Button
                      variant='contained'
                      size='small'
                      onClick={handleCommentSubmit}
                      disabled={isSubmitting || !newComment.trim()}
                      sx={{
                        borderRadius: 'var(--ds-radius-md)',
                        textTransform: 'none',
                        fontWeight: 'var(--ds-font-weight-medium)',
                        backgroundColor: 'var(--nb-btn-primary)',
                        '&:hover': {
                          backgroundColor: 'var(--nb-btn-primary-hover)',
                        },
                        '&:disabled': {
                          backgroundColor: 'var(--nb-btn-primary-disabled)',
                        },
                      }}
                    >
                      {isSubmitting ? 'Submitting...' : 'Add Comment'}
                    </Button>
                  </Box>
                )}
              </Box>
            </Box>
          </Grid>
        )}

        {/* Non-Jira: comments managed externally */}
        {!isJiraTicket && ticketData?.url && (
          <Grid item xs={12}>
            <Box sx={{ textAlign: 'center', py: 'var(--ds-space-4)' }}>
              <Typography variant='body2' sx={{ color: 'var(--ds-gray-600)' }}>
                Comments are managed in {toolDisplayName}.{' '}
                <a
                  href={ticketData.url}
                  target='_blank'
                  rel='noopener noreferrer'
                  style={{ color: 'var(--ds-blue-500)', fontWeight: 'var(--ds-font-weight-medium)' }}
                >
                  View ticket
                </a>
              </Typography>
            </Box>
          </Grid>
        )}
      </Grid>
    </Box>
  );
};

TicketDetailsComponent.propTypes = {
  ticketData: PropTypes.shape({
    platform: PropTypes.string,
    url: PropTypes.string,
    ticket_id: PropTypes.string,
    account_id: PropTypes.string,
    integration_id: PropTypes.string,
    description: PropTypes.string,
    ticket_type: PropTypes.string,
    source: PropTypes.string,
    reference_id: PropTypes.string,
    updated_at: PropTypes.string,
    project_key: PropTypes.string,
  }),
  accountsData: PropTypes.array,
};

const getAccountDisplayName = (accountsData, accountId) => {
  const account = accountsData?.find((acc) => acc.id === accountId);
  return account?.account_name || accountId || '-';
};

const TicketListTable = ({
  id = 'all-tickets',
  defaultQuery = {},
  enableAssigneeFilter = true,
  selectedPriority,
  statusFilter,
  setStatusFilter,
  selectedStatus,
  assigneeFilter,
  setAssigneeFilter,
  selectedAssignee,
  selectedTitle,
  onPriorityFilterChange,
  onStatusFilterChange,
  onAssigneeFilterChange,
  onTitleFilterChange,

  toolFilter,
  setToolFilter,
  selectedTool,
  onToolFilterChange,

  accountFilter,
  setAccountFilter,
  selectedAccount,
  onAccountFilterChange,
  onClearAllFilters,
}) => {
  const [tickets, setTickets] = useState([]);
  const [totalCount, setTotalCount] = useState(0);
  const [currentPage, setCurrentPage] = useState(0);
  const [priorityFilter, setPriorityFilter] = useState([]);
  const [loading, setLoading] = useState(false);
  const [recordsPerPage, setRecordsPerPage] = useState(10);
  const [accountsData, setAccountsData] = useState([]);
  const [titleInput, setTitleInput] = useState(selectedTitle || '');
  const [searchByTitle, setSearchByTitle] = useState(selectedTitle || '');

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setRecordsPerPage(limit);
  };

  const handleOpenTicket = useCallback((url) => window.open(url, '_blank'), []);

  const listTickets = () => {
    let query = {
      severity: selectedPriority,
      assignee: selectedAssignee || defaultQuery?.assignee,
      createdBy: defaultQuery?.createdBy,
      status: selectedStatus,
      title: searchByTitle,
      tool: selectedTool,
      account_id: selectedAccount,
    };
    setLoading(true);
    ticketsApi
      .listTickets({ limit: recordsPerPage, offset: currentPage * recordsPerPage, where: query })
      .then((res) => {
        setTotalCount(res?.data?.count);
        setTickets(res?.data?.tickets || []);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  const data = useMemo(() => {
    return tickets.map((item) => [
      {
        component: (
          <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
            <Typography
              noWrap
              sx={{
                fontSize: 'var(--ds-text-body)',
                color: 'var(--ds-blue-500)',
                cursor: 'pointer',
                '&:hover': { textDecoration: 'underline' },
              }}
              onClick={(e) => {
                e.stopPropagation();
                handleOpenTicket(item?.url);
              }}
            >
              {item.ticket_id}
            </Typography>
            <CopyButton text={item.ticket_id} size='xs' />
          </Box>
        ),
        drilldownQuery: {
          ticket_id: item.ticket_id,
          id: item.id,
          ticketData: item,
        },
      },
      {
        component: (
          <Tooltip title={TOOL_DISPLAY_NAMES[item.platform?.toLowerCase()] || item.platform || '-'} arrow placement='top'>
            <Box sx={{ display: 'inline-flex' }}>
              <CloudProviderIcon cloud_provider={item.platform} width='20px' height='20px' />
            </Box>
          </Tooltip>
        ),
      },
      { component: <Text value={item.title || '-'} showAutoEllipsis sx={{ minWidth: ds.space.mul(0, 45) }} /> },
      { component: <SeverityIcon level={toDsSeverityLevel(item.severity || 'Critical')} />, data: item.severity || 'Critical' },
      { component: <Label text={item.status} /> },
      { component: <Text value={getAccountDisplayName(accountsData, item.account_id)} showAutoEllipsis /> },
      { component: <Text value={item.user?.display_name || '-'} /> },
      { component: <Text value={item?.assignee || '-'} /> },
      { component: <Datetime value={item.created_at} /> },
    ]);
  }, [tickets, accountsData, handleOpenTicket]);

  // Main data fetch effect
  useEffect(() => {
    listTickets();
  }, [
    currentPage,
    recordsPerPage,
    selectedPriority,
    selectedAssignee,
    selectedStatus,
    searchByTitle,
    defaultQuery?.assignee,
    defaultQuery?.createdBy,
    selectedTool,
    selectedAccount,
  ]);

  // Initial filter options fetch
  useEffect(() => {
    ticketsApi.listPriority().then((res) => {
      const sortedPriorities = res?.data?.priority?.sort((a, b) => customPriorityOrder.indexOf(a) - customPriorityOrder.indexOf(b));
      setPriorityFilter(sortedPriorities);
    });
    ticketsApi.listStatus().then((res) => {
      setStatusFilter(res?.data?.status);
    });
    ticketsApi.listAssignee().then((res) => {
      setAssigneeFilter(res?.data?.assignee);
    });
    ticketsApi.listTool().then((res) => {
      setToolFilter(res?.data?.tool);
    });

    // Fetch all accounts once at startup (using lightweight query without date dependencies)
    apiAccount
      .getAccountTypes()
      .then((res) => {
        const accounts = res?.data?.all_accounts || [];
        setAccountsData(accounts);
        if (setAccountFilter) {
          setAccountFilter(accounts.map((acc) => ({ label: acc.account_name, value: acc.id })));
        }
      })
      .catch(() => {
        setAccountsData([]);
      });
  }, []);

  const handleAccountFilterChange = (...args) => {
    setCurrentPage(0);
    onAccountFilterChange(...args);
  };

  const handlePriorityFilterChange = (...args) => {
    setCurrentPage(0);
    onPriorityFilterChange(...args);
  };

  const handleToolFilterChange = (...args) => {
    setCurrentPage(0);
    onToolFilterChange(...args);
  };

  const handleStatusFilterChange = (...args) => {
    setCurrentPage(0);
    onStatusFilterChange(...args);
  };

  const handleAssigneeFilterChange = (...args) => {
    setCurrentPage(0);
    onAssigneeFilterChange(...args);
  };

  const hasActiveFilters = !!(selectedPriority || selectedStatus || selectedAssignee || selectedTool || selectedAccount || selectedTitle);

  return (
    <ListingLayout id='list-ticket'>
      <ListingLayout.Toolbar actions={<DownloadButton onClick={() => ({ tableId: id })} />}>
        <FilterDropdown
          id='ticket-filter-account'
          label='Account'
          options={accountFilter || []}
          value={selectedAccount}
          onSelect={handleAccountFilterChange}
        />
        <FilterDropdown
          id='ticket-filter-priority'
          label='Priority'
          options={priorityFilter}
          value={selectedPriority}
          onSelect={handlePriorityFilterChange}
        />
        <FilterDropdown id='ticket-filter-tool' label='Tool' options={toolFilter} value={selectedTool} onSelect={handleToolFilterChange} />
        <FilterDropdown id='ticket-filter-status' label='Status' options={statusFilter} value={selectedStatus} onSelect={handleStatusFilterChange} />
        {enableAssigneeFilter && (
          <FilterDropdown
            id='ticket-filter-assignee'
            label='Assignee'
            options={assigneeFilter}
            value={selectedAssignee}
            onSelect={handleAssigneeFilterChange}
          />
        )}
        <CustomSearch
          id='ticket-filter-title'
          value={titleInput}
          onChange={(next) => {
            setTitleInput((prev) => {
              if (prev.trim() !== '' && next.trim() === '') {
                setSearchByTitle('');
                setCurrentPage(0);
                onTitleFilterChange({ target: { value: '' } });
              }
              return next;
            });
          }}
          onEnterPress={() => {
            setSearchByTitle(titleInput);
            setCurrentPage(0);
            onTitleFilterChange({ target: { value: titleInput } });
          }}
          onClear={() => {
            setTitleInput('');
            setCurrentPage(0);
            onTitleFilterChange({ target: { value: '' } });
          }}
          label='Title'
        />
        {hasActiveFilters && (
          <Typography
            data-testid='clear-all-filters'
            onClick={onClearAllFilters}
            sx={{
              fontSize: 'var(--ds-text-small)',
              fontWeight: 'var(--ds-font-weight-medium)',
              color: 'var(--ds-blue-500)',
              cursor: 'pointer',
              whiteSpace: 'nowrap',
              alignSelf: 'center',
              '&:hover': { textDecoration: 'underline' },
            }}
          >
            Clear All
          </Typography>
        )}
      </ListingLayout.Toolbar>
      <ListingLayout.Body>
        <CustomTable2
          tableHeadingCenter={['Priority']}
          id={id}
          headers={TICKET_HEADERS}
          tableData={data}
          rowsPerPage={recordsPerPage}
          onPageChange={onPageChange}
          totalRows={totalCount}
          loading={loading}
          pageNumber={currentPage + 1}
          expandable={{
            tabs: [
              {
                key: 'details',
                text: 'Ticket Details',
                componentFn: (_, drilldownQuery) => {
                  const ticketData = drilldownQuery?.ticketData;
                  return <TicketDetailsComponent ticketData={ticketData} accountsData={accountsData} />;
                },
              },
            ],
          }}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default TicketListTable;

TicketListTable.propTypes = {
  heading: PropTypes.string,
  id: PropTypes.string,
  defaultQuery: PropTypes.any,
  enableAssigneeFilter: PropTypes.bool,
  selectedPriority: PropTypes.any,
  statusFilter: PropTypes.any,
  setStatusFilter: PropTypes.any,
  toolFilter: PropTypes.array,
  setToolFilter: PropTypes.any,
  selectedTool: PropTypes.any,
  onToolFilterChange: PropTypes.func,
  selectedStatus: PropTypes.any,
  assigneeFilter: PropTypes.any,
  setAssigneeFilter: PropTypes.any,
  selectedAssignee: PropTypes.any,
  selectedTitle: PropTypes.any,
  onPriorityFilterChange: PropTypes.any,
  onStatusFilterChange: PropTypes.any,
  onAssigneeFilterChange: PropTypes.any,
  onTitleFilterChange: PropTypes.any,
  accountFilter: PropTypes.array,
  setAccountFilter: PropTypes.func,
  selectedAccount: PropTypes.any,
  onAccountFilterChange: PropTypes.func,
  onClearAllFilters: PropTypes.func,
};
