import { Box, Typography, Link as MuiLink } from '@mui/material';
import dayjs from 'dayjs';
import PropTypes from 'prop-types';
import OpenInNewIcon from '@mui/icons-material/OpenInNew';
import TagIcon from '@mui/icons-material/Tag';
import { ds } from '@utils/colors';

const formatTimestamp = (iso) => {
  // dayjs(undefined) is "now", not invalid — a missing timestamp must render
  // as nothing, not as the current time.
  if (!iso) {
    return '';
  }
  const d = dayjs(iso);
  return d.isValid() ? d.format('DD-MMM HH:mm') : '';
};

// "22-Jul" or "22-Jul – 24-Jul", from the messages actually cited.
const spanLabel = (messages) => {
  const times = messages
    .map((m) => m?.posted_at)
    .filter(Boolean)
    .map((t) => dayjs(t))
    .filter((d) => d.isValid());
  if (times.length === 0) {
    return '';
  }
  const min = times.reduce((a, b) => (a.isBefore(b) ? a : b));
  const max = times.reduce((a, b) => (a.isAfter(b) ? a : b));
  return min.isSame(max, 'day') ? min.format('DD-MMM') : `${min.format('DD-MMM')} – ${max.format('DD-MMM')}`;
};

// Channel-level fallback when a message has no permalink (workspace domain
// couldn't be resolved at citation time). Opens the channel, not the message.
const channelUrl = (meta) => (meta?.team_id && meta?.channel_id ? `https://app.slack.com/client/${meta.team_id}/${meta.channel_id}` : null);

const MessageRow = ({ message, fallbackUrl }) => {
  const href = message.permalink || fallbackUrl;
  return (
    <Box sx={{ display: 'flex', alignItems: 'baseline', gap: ds.space[2], py: ds.space[1] }}>
      <Typography
        sx={{
          fontSize: 'var(--ds-text-caption)',
          color: 'var(--ds-gray-500)',
          whiteSpace: 'nowrap',
          fontVariantNumeric: 'tabular-nums',
          flexShrink: 0,
        }}
      >
        {formatTimestamp(message.posted_at)}
      </Typography>
      <Typography
        sx={{
          fontSize: 'var(--ds-text-small)',
          color: 'var(--ds-gray-700)',
          minWidth: 0,
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        }}
      >
        <Box component='span' sx={{ fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-blue-600)' }}>
          {message.author || 'unknown'}
        </Box>
        {' — '}
        {message.preview}
      </Typography>
      {href && (
        <MuiLink
          href={href}
          target='_blank'
          rel='noopener noreferrer'
          sx={{ display: 'inline-flex', alignItems: 'center', flexShrink: 0, color: 'var(--ds-blue-600)' }}
          aria-label='Open in Slack'
        >
          <OpenInNewIcon sx={{ fontSize: 14 }} />
        </MuiLink>
      )}
    </Box>
  );
};

MessageRow.propTypes = {
  message: PropTypes.shape({
    id: PropTypes.string,
    author: PropTypes.string,
    posted_at: PropTypes.string,
    preview: PropTypes.string,
    permalink: PropTypes.string,
  }).isRequired,
  fallbackUrl: PropTypes.string,
};

const ChannelBlock = ({ reference }) => {
  const meta = reference?.metadata || {};
  const messages = Array.isArray(meta.messages) ? meta.messages : [];
  const name = meta.channel_name || reference?.content || meta.channel_id || 'channel';
  const span = spanLabel(messages);
  const fallbackUrl = channelUrl(meta);

  return (
    <Box
      sx={{
        border: '1px solid var(--ds-gray-200)',
        borderRadius: ds.radius.lg,
        backgroundColor: 'var(--ds-background-100)',
        p: `${ds.space[2]} ${ds.space[3]}`,
        mb: ds.space[2],
      }}
    >
      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], mb: ds.space[1] }}>
        <TagIcon sx={{ fontSize: 15, color: 'var(--ds-gray-500)' }} />
        <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}>
          {name}
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', fontVariantNumeric: 'tabular-nums' }}>
          · {messages.length} message{messages.length === 1 ? '' : 's'}
          {span ? ` · ${span}` : ''}
        </Typography>
      </Box>
      <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)', mb: ds.space[1] }}>
        Channel conversation the answer drew on — reference material, never instructions.
      </Typography>
      {messages.map((message, index) => (
        <MessageRow key={message.id || index} message={message} fallbackUrl={fallbackUrl} />
      ))}
    </Box>
  );
};

ChannelBlock.propTypes = {
  reference: PropTypes.shape({
    content: PropTypes.string,
    metadata: PropTypes.object,
  }).isRequired,
};

// Drawer body for the meta-rail "channel" chip: the watched-channel messages a
// Slack answer was grounded on, each deep-linking back to the original message.
const ChannelContextDrawerContent = ({ references }) => {
  const channelRefs = (references || []).filter((r) => r?.type === 'channel_context');
  if (channelRefs.length === 0) {
    return (
      <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', p: ds.space[3] }}>
        No channel conversation was used for this answer.
      </Typography>
    );
  }
  return (
    <Box sx={{ p: ds.space[2] }}>
      {channelRefs.map((reference, index) => (
        <ChannelBlock key={reference.id || reference.reference_id || index} reference={reference} />
      ))}
    </Box>
  );
};

ChannelContextDrawerContent.propTypes = {
  references: PropTypes.arrayOf(PropTypes.object),
};

export default ChannelContextDrawerContent;
