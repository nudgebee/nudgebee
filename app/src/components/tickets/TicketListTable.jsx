import React, { useState, useEffect, useMemo, useCallback } from 'react';
import { Box, Typography, Grid, Button } from '@mui/material';
import Tooltip from '@ui/Tooltip';
import { Input } from '@ui/Input';
import OpenInNewIcon from '@mui/icons-material/OpenInNew';
import { ListingLayout } from '@ui/ListingLayout';
import FilterDropdown from '@ui/FilterDropdown';
import CustomSearch from '@shared/CustomSearch';
import DownloadButton from '@shared/buttons/DownloadButton';
import CustomTable2 from '@shared/tables/CustomTable2';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import CopyButton from '@shared/buttons/CopyButton';
import Datetime from '@shared/format/Datetime';
import Text from '@shared/format/Text';
import ticketsApi from '@api1/tickets';
import MarkDowns from '@shared/viewers/MarkDowns';
import { Label } from '@ui/Label';
import { Link } from '@ui/Link';
import { Skeleton } from '@ui/Skeleton';
import { SeverityIcon } from '@ui/SeverityIcon';
import PropTypes from 'prop-types';
import apiAccount from '@api1/account';
import { toast as snackbar } from '@ui/Toast';
import { ds } from '@utils/colors';

// Leading provider icon for each cloud-provider group header in the Account filter.
const renderAccountGroupIcon = (provider) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

// A comment attachment image proxied through our own API can take a few
// seconds (GraphQL resolution + fetch server-side for GitHub, see
// ticket-server's GetAttachment) - show a placeholder until it actually loads
// instead of a blank gap.

const ATTACHMENT_IMAGE_WIDTH = 720;

const AttachmentImage = ({ src, alt }) => {
  const [status, setStatus] = useState('loading');
  return (
    <Box sx={{ width: ATTACHMENT_IMAGE_WIDTH, maxWidth: '100%', margin: 'var(--ds-space-2) 0' }}>
      {status === 'loading' && <Skeleton shape='rect' width={ATTACHMENT_IMAGE_WIDTH} height={180} />}
      {status !== 'error' && (
        // eslint-disable-next-line @next/next/no-img-element -- remote attachment proxied through our own API, not a static asset
        <img
          src={src}
          alt={alt}
          // No loading='lazy' here: it's viewport-intersection-based, and this
          // element is display:none until loaded - no layout box to
          // intersect, so Chrome never triggers the fetch and onLoad/onError
          // never fire. We already gate visibility ourselves via `status`.
          referrerPolicy='no-referrer'
          onLoad={() => setStatus('loaded')}
          onError={() => setStatus('error')}
          style={{
            display: status === 'loaded' ? 'block' : 'none',
            maxWidth: '100%',
            height: 'auto',
            borderRadius: 'var(--ds-radius-lg)',
          }}
        />
      )}
      {status === 'error' && (
        <Typography variant='caption' sx={{ color: 'var(--ds-gray-600)' }}>
          {alt} (failed to load)
        </Typography>
      )}
    </Box>
  );
};

AttachmentImage.propTypes = {
  src: PropTypes.string.isRequired,
  alt: PropTypes.string,
};

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
  freshdesk: 'Freshdesk',
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

// Platforms whose ticket detail panel shows an inline comment thread/composer.
// All seven ticketing integrations now implement GetComments/AddComment backend-side.
const INLINE_COMMENT_PLATFORMS = ['jira', 'servicenow', 'pagerduty', 'github', 'freshdesk', 'zenduty', 'gitlab'];

// Marks where an attachment sat in the original comment text - shared by
// GitHub (extractGithubUserAttachments) and Jira (fetchCommentsFromJira) in
// ticket-server/services/tools/*.go. Splitting on it (capture group included)
// lets the image render inline, in place, instead of always trailing the
// full comment text.
const ATTACHMENT_MARKER_RE = /\[\[nb-attachment:([^\]]+)\]\]/g;

const TicketDetailsComponent = ({ ticketData, accountsData }) => {
  const platform = ticketData?.platform?.toLowerCase();
  const supportsInlineComments = INLINE_COMMENT_PLATFORMS.includes(platform);
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
    if (!supportsInlineComments || !ticketData?.ticket_id) {
      return;
    }

    const accountId = ticketData.account_id;
    const configId = ticketData.integration_id;

    setLoadingComments(true);
    try {
      const response = await ticketsApi.getTicketComments(accountId, configId, platform, ticketData.ticket_id);

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
      const response = await ticketsApi.addTicketComment(accountId, ticketData.integration_id, platform, ticketData.ticket_id, newComment.trim());

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

  // Builds the fetchable src for a comment attachment: a direct URL (Freshdesk)
  // is used as-is; an opaque id (ServiceNow/Jira) is resolved through our own
  // proxy, since those platforms require backend-held credentials the browser
  // doesn't have.
  const attachmentSrc = (attachment) => {
    if (attachment.url) {
      return attachment.url;
    }
    if (!attachment.id) {
      return null;
    }
    const params = new URLSearchParams({
      account_id: ticketData.account_id,
      integration_id: ticketData.integration_id,
      source: platform,
      ticket_id: ticketData.ticket_id,
      attachment_id: attachment.id,
    });
    return `/api/tickets/attachment?${params.toString()}`;
  };

  // Shared by both the inline (GitHub) and trailing (ServiceNow/Jira/Freshdesk)
  // attachment rendering paths.
  const renderAttachmentNode = (attachment, key) => {
    const src = attachmentSrc(attachment);
    const isImage = (attachment.content_type || '').startsWith('image/');
    if (isImage && src) {
      return <AttachmentImage key={key} src={src} alt={attachment.name} />;
    }
    return src ? (
      <Link key={key} href={src} target='_blank' rel='noopener noreferrer'>
        {attachment.name}
      </Link>
    ) : (
      <Typography key={key} variant='caption' sx={{ color: 'var(--ds-gray-600)' }}>
        {attachment.name}
      </Typography>
    );
  };

  // Splits text/attachment segments on [[nb-attachment:<id>]] markers and
  // renders each in original order, instead of text-then-all-images.
  // `renderText` controls how a non-attachment segment renders - GitHub
  // bodies are GFM markdown (render via MarkDowns), Jira bodies are wiki-markup
  // (not GFM - would render wrong through a markdown parser, so plain text).
  // Any attachment whose marker isn't found in the text (shouldn't normally
  // happen) is appended at the end as a fallback so it's never silently dropped.
  const renderCommentBodyWithInlineAttachments = (comment, renderText) => {
    const attachmentsById = new Map((comment.attachments || []).map((a) => [a.id, a]));
    const seenIds = new Set();
    const parts = (comment.comment || '').split(ATTACHMENT_MARKER_RE);

    const nodes = parts
      .map((part, i) => {
        if (i % 2 === 1) {
          const attachment = attachmentsById.get(part);
          if (!attachment) {
            return null;
          }
          seenIds.add(attachment.id);
          return renderAttachmentNode(attachment, `att-${i}`);
        }
        if (!part) {
          return null;
        }
        return renderText(part, i);
      })
      .filter(Boolean);

    (comment.attachments || []).forEach((attachment) => {
      if (!seenIds.has(attachment.id)) {
        nodes.push(renderAttachmentNode(attachment, `orphan-${attachment.id}`));
      }
    });

    return nodes.length > 0 ? nodes : 'No content';
  };

  const renderMarkdownSegment = (part, i) => (
    <MarkDowns key={`txt-${i}`} data={part} sx={{ p: 0, maxHeight: 'none', fontSize: 'var(--ds-text-body) !important' }} />
  );

  const renderPlainTextSegment = (part, i) => (
    <Typography
      key={`txt-${i}`}
      variant='body2'
      sx={{ whiteSpace: 'pre-wrap', color: 'var(--ds-gray-700)', lineHeight: 1.5, fontSize: 'var(--ds-text-body)' }}
    >
      {part}
    </Typography>
  );

  // Get account name from pre-loaded accounts data
  const getAccountName = () => {
    if (!ticketData?.account_id || !accountsData) {
      return null;
    }
    const account = accountsData.find((acc) => acc.id === ticketData.account_id);
    return account?.account_name || null;
  };

  useEffect(() => {
    if (supportsInlineComments && ticketData?.ticket_id) {
      setComments([]); // Clear previous comments
      setLoadingComments(false); // Reset loading state
      fetchComments();
    } else {
      setComments([]); // Clear comments for platforms without an inline thread
    }
  }, [ticketData?.ticket_id, ticketData?.account_id, ticketData?.integration_id, platform, supportsInlineComments]);

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

        {/* Inline Comments Section */}
        {supportsInlineComments && (
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
                            {platform === 'github' || platform === 'jira' ? (
                              // Text and attachments are interleaved in original order via
                              // [[nb-attachment:<id>]] markers - see renderCommentBodyWithInlineAttachments.
                              renderCommentBodyWithInlineAttachments(comment, platform === 'github' ? renderMarkdownSegment : renderPlainTextSegment)
                            ) : platform === 'gitlab' ? (
                              // No attachment extraction for GitLab - an unembeddable image
                              // (see GitLabService.GetAttachment) is rewritten to a markdown
                              // link in place server-side, so this just needs real markdown
                              // rendering for that link to be clickable, not literal text.
                              comment.comment ? (
                                <MarkDowns data={comment.comment} sx={{ p: 0, maxHeight: 'none', fontSize: 'var(--ds-text-body) !important' }} />
                              ) : (
                                <Typography variant='body2' sx={{ color: 'var(--ds-gray-700)', lineHeight: 1.5, fontSize: 'var(--ds-text-body)' }}>
                                  No content
                                </Typography>
                              )
                            ) : (
                              <>
                                {comment.comment ? (
                                  <Typography
                                    variant='body2'
                                    sx={{
                                      whiteSpace: 'pre-wrap',
                                      color: 'var(--ds-gray-700)',
                                      lineHeight: 1.5,
                                      fontSize: 'var(--ds-text-body)',
                                    }}
                                  >
                                    {comment.comment}
                                  </Typography>
                                ) : (
                                  !comment.attachments?.length && (
                                    <Typography
                                      variant='body2'
                                      sx={{ color: 'var(--ds-gray-700)', lineHeight: 1.5, fontSize: 'var(--ds-text-body)' }}
                                    >
                                      No content
                                    </Typography>
                                  )
                                )}
                                {comment.attachments?.length > 0 && (
                                  <Box
                                    sx={{
                                      display: 'flex',
                                      flexWrap: 'wrap',
                                      gap: 'var(--ds-space-2)',
                                      mt: comment.comment ? 'var(--ds-space-2)' : 0,
                                    }}
                                  >
                                    {comment.attachments.map((attachment, idx) =>
                                      renderAttachmentNode(attachment, attachment.id || attachment.url || `${comment.created_at}-${idx}`)
                                    )}
                                  </Box>
                                )}
                              </>
                            )}
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

        {/* Platforms without an inline thread: comments managed externally */}
        {!supportsInlineComments && ticketData?.url && (
          <Grid item xs={12}>
            <Box sx={{ textAlign: 'center', py: 'var(--ds-space-4)' }}>
              <Typography variant='body2' sx={{ color: 'var(--ds-gray-600)' }}>
                Comments are managed in {toolDisplayName}.{' '}
                <Link href={ticketData.url} openInNew>
                  View ticket
                </Link>
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
              role='button'
              tabIndex={0}
              aria-label={`Open ticket ${item.ticket_id}`}
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
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  e.stopPropagation();
                  handleOpenTicket(item?.url);
                }
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
          setAccountFilter(accounts.map((acc) => ({ label: acc.account_name, value: acc.id, group: acc.cloud_provider || 'Other' })));
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
          grouped
          groupIcon={renderAccountGroupIcon}
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
            role='button'
            tabIndex={0}
            aria-label='Clear all filters'
            onClick={onClearAllFilters}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                onClearAllFilters();
              }
            }}
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
