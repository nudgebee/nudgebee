import React, { useState, useEffect, useMemo, useCallback } from 'react';
import { Box, Typography, Grid, TextField, Button, Tooltip } from '@mui/material';
import OpenInNewIcon from '@mui/icons-material/OpenInNew';
import BoxLayout2 from '@components1/common/BoxLayout2';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import CloudProviderIcon from '@components1/common/CloudIcon';
import CopyableText from '@components1/common/CopyableText';
import Datetime from '@components1/common/format/Datetime';
import Text from '@components1/common/format/Text';
import SeverityIcon from '@components1/common/widgets/SeverityIcon';
import ticketsApi from '@api1/tickets';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import PropTypes from 'prop-types';
import apiAccount from '@api1/account';
import { snackbar } from '@components1/common/snackbarService';
import { colors } from 'src/utils/colors';

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
    <Box sx={{ p: 2, backgroundColor: 'white', borderRadius: 3 }}>
      {/* Quick action: Open in external tool */}
      {ticketData?.url && (
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', mb: 1 }}>
          <Button
            variant='outlined'
            size='small'
            startIcon={<OpenInNewIcon sx={{ fontSize: '16px' }} />}
            href={ticketData.url}
            target='_blank'
            data-testid='open-in-tool-btn'
            sx={{
              textTransform: 'none',
              fontWeight: 500,
              fontSize: '12px',
              borderRadius: '6px',
              borderColor: colors.border.secondary,
              color: colors.text.secondary,
              '&:hover': {
                borderColor: colors.border.primary,
                backgroundColor: colors.background.tertiaryLightest,
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
          <Typography variant='subtitle2' sx={{ fontWeight: 'bold', mb: 1 }}>
            Description
          </Typography>
          {(() => {
            const parsedData = parseDescription(ticketData?.description);
            if (parsedData.length > 0) {
              return (
                <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0.5 }}>
                  {parsedData.map((item, index) => (
                    <Typography key={index} variant='body2'>
                      <strong>{item.key}:</strong> {item.value}
                    </Typography>
                  ))}
                </Box>
              );
            }
            return (
              <Typography variant='body2' sx={{ mb: 2 }}>
                {ticketData?.description || 'No description available'}
              </Typography>
            );
          })()}
        </Grid>

        {/* Additional Details - structured card layout */}
        <Grid item xs={12} md={6}>
          <Typography variant='subtitle2' sx={{ fontWeight: 'bold', mb: 1 }}>
            Additional Details
          </Typography>
          <Box
            sx={{
              display: 'grid',
              gridTemplateColumns: '130px 1fr',
              gap: '6px 12px',
              p: 1.5,
              backgroundColor: colors.background.tertiaryLightestest,
              borderRadius: '6px',
            }}
          >
            {ticketData?.ticket_type && (
              <>
                <Typography variant='body2' sx={{ color: colors.text.tertiary, fontWeight: 500, fontSize: '12px' }}>
                  Ticket Type
                </Typography>
                <Typography variant='body2' sx={{ fontSize: '12px' }}>
                  {ticketData.ticket_type}
                </Typography>
              </>
            )}
            {ticketData?.source && (
              <>
                <Typography variant='body2' sx={{ color: colors.text.tertiary, fontWeight: 500, fontSize: '12px' }}>
                  Source
                </Typography>
                <Typography variant='body2' sx={{ fontSize: '12px' }}>
                  {ticketData.source}
                </Typography>
              </>
            )}
            {ticketData?.reference_id && (
              <>
                <Typography variant='body2' sx={{ color: colors.text.tertiary, fontWeight: 500, fontSize: '12px' }}>
                  Reference ID
                </Typography>
                <Typography variant='body2' sx={{ fontSize: '12px' }}>
                  {ticketData.reference_id}
                </Typography>
              </>
            )}
            {ticketData?.updated_at && (
              <>
                <Typography variant='body2' sx={{ color: colors.text.tertiary, fontWeight: 500, fontSize: '12px' }}>
                  Last Updated
                </Typography>
                <Typography variant='body2' sx={{ fontSize: '12px' }}>
                  <Datetime value={ticketData.updated_at} />
                </Typography>
              </>
            )}
            {ticketData?.project_key && (
              <>
                <Typography variant='body2' sx={{ color: colors.text.tertiary, fontWeight: 500, fontSize: '12px' }}>
                  Project Key
                </Typography>
                <Typography variant='body2' sx={{ fontSize: '12px' }}>
                  {ticketData.project_key}
                </Typography>
              </>
            )}
            {ticketData?.account_id && (
              <>
                <Typography variant='body2' sx={{ color: colors.text.tertiary, fontWeight: 500, fontSize: '12px' }}>
                  Account
                </Typography>
                <Typography variant='body2' sx={{ fontSize: '12px' }}>
                  {getAccountName() || ticketData.account_id}
                </Typography>
              </>
            )}
          </Box>
        </Grid>

        {/* Jira Comments Section */}
        {isJiraTicket && (
          <Grid item xs={12}>
            <Typography variant='subtitle2' sx={{ fontWeight: 600, mb: 1.5, color: colors.text.secondary }}>
              Comments
            </Typography>
            <Box
              sx={{
                border: `1px solid ${colors.border.secondaryLightest}`,
                borderRadius: '8px',
                backgroundColor: colors.background.white,
                boxShadow: '0px 2px 8px rgba(0, 0, 0, 0.04)',
                overflow: 'hidden',
              }}
            >
              {/* Existing Comments Display */}
              <Box
                sx={{
                  p: 2,
                  maxHeight: '350px',
                  overflowY: 'auto',
                  backgroundColor: colors.background.tertiaryLightestest,
                  '&::-webkit-scrollbar': {
                    width: '6px',
                  },
                  '&::-webkit-scrollbar-track': {
                    backgroundColor: colors.background.tertiaryLightest,
                  },
                  '&::-webkit-scrollbar-thumb': {
                    backgroundColor: colors.border.secondary,
                    borderRadius: '3px',
                  },
                }}
              >
                {loadingComments ? (
                  <Typography variant='body2' sx={{ color: colors.text.tertiary, fontStyle: 'italic' }}>
                    Loading comments...
                  </Typography>
                ) : comments.length > 0 ? (
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: 0 }}>
                    {comments.map((comment, index) => (
                      <Box
                        key={index}
                        sx={{
                          py: 1.5,
                          px: 1,
                          borderBottom: index < comments.length - 1 ? `1px solid ${colors.border.secondaryLightest}` : 'none',
                          transition: 'background-color 0.2s ease',
                          '&:hover': {
                            backgroundColor: colors.background.white,
                          },
                        }}
                      >
                        <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 1 }}>
                          <Box
                            sx={{
                              minWidth: 28,
                              width: 28,
                              height: 28,
                              borderRadius: '50%',
                              backgroundColor: colors.background.primaryLightest,
                              display: 'flex',
                              alignItems: 'center',
                              justifyContent: 'center',
                              fontWeight: 600,
                              fontSize: '12px',
                              color: colors.text.primary,
                              flexShrink: 0,
                            }}
                          >
                            {(comment.author || 'U').charAt(0).toUpperCase()}
                          </Box>
                          <Box sx={{ flex: 1, minWidth: 0 }}>
                            <Box sx={{ display: 'flex', alignItems: 'baseline', gap: 1, mb: 0.5, flexWrap: 'wrap' }}>
                              <Typography variant='body2' sx={{ fontWeight: 600, color: colors.text.secondary, fontSize: '13px' }}>
                                {comment.author || 'Unknown'}
                              </Typography>
                              <Typography variant='caption' sx={{ color: colors.text.tertiary, fontSize: '11px' }}>
                                {comment.created_at ? <Datetime value={comment.created_at} /> : 'Unknown date'}
                              </Typography>
                              {comment.updated_at && comment.updated_at !== comment.created_at && (
                                <Typography variant='caption' sx={{ color: colors.text.tertiary, fontStyle: 'italic', fontSize: '11px' }}>
                                  • edited
                                </Typography>
                              )}
                            </Box>
                            <Typography
                              variant='body2'
                              sx={{
                                whiteSpace: 'pre-wrap',
                                color: colors.text.secondary,
                                lineHeight: 1.5,
                                fontSize: '13px',
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
                  <Box sx={{ textAlign: 'center', py: 3, px: 2 }}>
                    <Typography variant='body2' sx={{ color: colors.text.tertiary, fontStyle: 'italic' }}>
                      No comments yet. Be the first to add a comment!
                    </Typography>
                  </Box>
                )}
              </Box>

              {/* Add New Comment - compact input */}
              <Box
                sx={{
                  p: 2.5,
                  backgroundColor: colors.background.white,
                  borderTop: `1px solid ${colors.border.secondaryLightest}`,
                }}
              >
                <TextField
                  fullWidth
                  multiline
                  rows={commentFocused || newComment ? 3 : 1}
                  placeholder='Write a comment...'
                  value={newComment}
                  onChange={(e) => setNewComment(e.target.value)}
                  onFocus={() => setCommentFocused(true)}
                  onBlur={() => {
                    if (!newComment) setCommentFocused(false);
                  }}
                  variant='outlined'
                  size='small'
                  sx={{
                    mb: commentFocused || newComment ? 2 : 0,
                    '& .MuiOutlinedInput-root': {
                      backgroundColor: colors.background.tertiaryLightestest,
                      borderRadius: '6px',
                      '&:hover fieldset': {
                        borderColor: colors.border.primary,
                      },
                      '&.Mui-focused fieldset': {
                        borderColor: colors.border.primary,
                      },
                    },
                  }}
                />
                {(commentFocused || newComment) && (
                  <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 1.5 }}>
                    <Button
                      variant='outlined'
                      size='small'
                      onClick={() => {
                        setNewComment('');
                        setCommentFocused(false);
                      }}
                      disabled={isSubmitting}
                      sx={{
                        borderRadius: '6px',
                        textTransform: 'none',
                        fontWeight: 500,
                        borderColor: colors.border.secondary,
                        color: colors.text.tertiary,
                        '&:hover': {
                          borderColor: colors.border.primary,
                          backgroundColor: colors.background.tertiaryLightest,
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
                        borderRadius: '6px',
                        textTransform: 'none',
                        fontWeight: 500,
                        backgroundColor: colors.button.primary,
                        '&:hover': {
                          backgroundColor: colors.button.primaryHover,
                        },
                        '&:disabled': {
                          backgroundColor: colors.button.primaryDisabled,
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
            <Box sx={{ textAlign: 'center', py: 2 }}>
              <Typography variant='body2' sx={{ color: colors.text.tertiary }}>
                Comments are managed in {toolDisplayName}.{' '}
                <a href={ticketData.url} target='_blank' rel='noopener noreferrer' style={{ color: colors.text.primary, fontWeight: 500 }}>
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
  heading = 'Tickets',
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
      title: selectedTitle?.length > 2 ? selectedTitle : null,
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
          <CopyableText copyableText={item.ticket_id} iconPosition='end' iconSize={14}>
            <Typography
              noWrap
              sx={{
                fontSize: '13px',
                color: colors.text.primary,
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
          </CopyableText>
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
      { component: <Text value={item.title || '-'} showAutoEllipsis sx={{ minWidth: '90px' }} /> },
      { component: <SeverityIcon severityType={item.severity || 'Critical'} /> },
      { component: <CustomLabels text={item.status} /> },
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

  const onEnterPress = () => {
    if (currentPage === 0) {
      listTickets();
    } else {
      setCurrentPage(0);
    }
  };

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

  const handleTitleFilterChange = (...args) => {
    onTitleFilterChange(...args);
    if (selectedTitle && args[0]?.target?.value === '') {
      if (currentPage === 0) {
        // selectedTitle is not a dependency of the main fetch useEffect, so resetting
        // the page when already on page 0 won't trigger a re-fetch — call explicitly.
        listTickets();
      } else {
        setCurrentPage(0);
      }
    }
  };

  const hasActiveFilters = !!(selectedPriority || selectedStatus || selectedAssignee || selectedTool || selectedAccount || selectedTitle);

  return (
    <BoxLayout2
      id={'list-ticket'}
      heading={heading}
      filterOptions={[
        {
          type: 'dropdown',
          enabled: true,
          options: accountFilter || [],
          onSelect: handleAccountFilterChange,
          minWidth: '150px',
          label: 'Account',
          value: selectedAccount,
        },
        {
          type: 'dropdown',
          enabled: true,
          options: priorityFilter,
          onSelect: handlePriorityFilterChange,
          minWidth: '150px',
          label: 'Priority',
          value: selectedPriority,
        },
        {
          type: 'dropdown',
          enabled: true,
          options: toolFilter,
          onSelect: handleToolFilterChange,
          minWidth: '150px',
          label: 'Tool',
          value: selectedTool,
        },
        {
          type: 'dropdown',
          enabled: true,
          options: statusFilter,
          onSelect: handleStatusFilterChange,
          minWidth: '150px',
          label: 'Status',
          value: selectedStatus,
        },
        {
          type: 'dropdown',
          enabled: enableAssigneeFilter,
          options: assigneeFilter,
          onSelect: handleAssigneeFilterChange,
          minWidth: '150px',
          label: 'Assignee',
          value: selectedAssignee,
        },
        {
          type: 'search',
          enabled: true,
          onSelect: handleTitleFilterChange,
          minWidth: '150px',
          label: 'Title',
          onEnter: onEnterPress,
        },
        {
          type: 'custom',
          enabled: hasActiveFilters,
          key: 'clear-all',
          component: (
            <Typography
              data-testid='clear-all-filters'
              onClick={onClearAllFilters}
              sx={{
                fontSize: '12px',
                fontWeight: 500,
                color: colors.text.primary,
                cursor: 'pointer',
                whiteSpace: 'nowrap',
                alignSelf: 'center',
                '&:hover': { textDecoration: 'underline' },
              }}
            >
              Clear All
            </Typography>
          ),
        },
      ]}
      sharingOptions={{
        sharing: {
          enabled: true,
          onClick: null,
        },
        download: {
          enabled: true,
          onClick: () => {
            return {
              tableId: id,
            };
          },
        },
      }}
    >
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
    </BoxLayout2>
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
