import { WatchIcon } from '@assets';
import Tooltip from '@components1/ds/Tooltip';
import Datetime from '@common/format/Datetime';
import CopyButton from '@common-new/CopyButton';
import { Avatar, Box, Grid, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { getIcon } from './AgentIcon';
import SafeIcon from '@components1/common/SafeIcon';
import Duration from './Duration';
import { ds } from '@utils/colors';

function ConversationCollapsableCard({
  id,
  text,
  contentComponents,
  toolData,
  conversationCreatedAt = '',
  conversationUpdatedAt = '',
  showFullTextHandler = () => {
    // No-op default; consumers pass the handler when textLength is true.
  },
  textLength = false,
  headerActions = null,
}) {
  // Per-type padding — extracted to a variable to keep the JSX clean (Sonar S3358).
  let cardPadding = `${ds.space[3]} ${ds.space[4]} ${ds.space[3]} ${ds.space[4]}`;
  if (toolData.type === 'response') {
    cardPadding = `${ds.space[1]} ${ds.space[4]} ${ds.space[2]} ${ds.space[4]}`;
  } else if (toolData.type !== 'question') {
    cardPadding = `${ds.space[2]} ${ds.space.mul(0, 5)} ${ds.space[2]} ${ds.space[2]}`;
  }

  return (
    <Box
      id={id}
      sx={{
        background: (toolData.tool || toolData.type) === 'question' ? 'var(--ds-background-200)' : 'transparent',
        fontFamily: toolData.type === 'response' && '"Poppins", sans-serif',
        backgroundColor: (toolData.tool || toolData.type) === 'question' && 'var(--ds-background-200)',
        borderTop: 'none',
        borderBottom:
          (toolData.tool || toolData.type) === 'question'
            ? `0px`
            : (toolData.tool || toolData.type) === 'response'
            ? `none`
            : `0.8px solid var(--ds-gray-200)`,
        transition: 'all ease 0.2s',
        borderRadius: (toolData.tool || toolData.type) === 'question' ? ds.radius.xl : '0px',
        animation: `fadeIn 0.3s ease 0s both`,
        '@keyframes fadeIn': {
          '0%': {
            opacity: 0,
            transform: 'translateY(4px)',
          },
          '100%': {
            opacity: 1,
            transform: 'translateY(0)',
          },
        },
      }}
    >
      <Box
        onClick={(toolData.tool || toolData.type) === 'question' && textLength ? () => showFullTextHandler() : undefined}
        sx={{
          display: 'flex',
          flexDirection: 'column',
          padding: cardPadding,
          backgroundColor: toolData.type == 'question' ? '#F6F6F6' : 'var(--ds-background-100)',
          borderTop: 'none',
          borderRadius: ds.radius.xl,
          cursor: (toolData.tool || toolData.type) === 'question' && textLength ? 'pointer' : 'default',
        }}
      >
        <Box
          sx={{
            display: 'grid',
            alignItems: 'center',
            gridTemplateColumns: (toolData.tool || toolData.type) === 'question' ? '1fr' : '1fr auto',
            gap: ds.space[5],
            cursor: (toolData.tool || toolData.type) === 'question' && textLength ? 'pointer' : 'default',
            minHeight: '0px',
            boxSizing: 'border-box',
            backgroundColor:
              (toolData.tool || toolData.type) == 'question' || (toolData.tool || toolData.type) == 'followup-question' ? 'transparent' : '',
            width: 'auto',
            '@media (max-width: 768px)': {
              gridTemplateColumns: '1fr',
              gap: ds.space[2],
            },
          }}
        >
          <Grid
            item
            md={5}
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: ds.space[3],
            }}
          >
            {(toolData.tool || toolData.type) !== 'question' && (toolData.tool || toolData.type) !== 'response' && (
              <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: ds.space[1], flexShrink: 0 }}>
                {getIcon(toolData.tool || toolData.type) ? (
                  <Tooltip title={toolData.tool || toolData.type} placement='top'>
                    <SafeIcon
                      style={{
                        mixBlendMode: 'multiply',
                        width: ds.space.mul(1, 5),
                        height: ds.space.mul(1, 5),
                        objectFit: 'contain',
                      }}
                      src={getIcon(toolData.tool || toolData.type)}
                      alt={toolData.tool || toolData.type}
                      className='icon'
                      width={24}
                      height={24}
                    />
                  </Tooltip>
                ) : (
                  <Tooltip title={toolData.tool?.[0] || toolData.type} placement='top'>
                    <Avatar style={{ width: ds.space[5], height: ds.space[5] }}>{(toolData.tool?.[0] || toolData.type).toUpperCase()}</Avatar>
                  </Tooltip>
                )}
              </Box>
            )}
            <Box width={'100%'}>
              {/* For response messages the title is empty (only the meta-rail occupies the
                  header row), so we skip the Typography entirely to avoid its baseline
                  line-height pushing the row taller than the meta-rail content. */}
              {toolData.type !== 'response' && (
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-body-lg)',
                    color: 'var(--ds-gray-700)',
                    fontFamily: toolData?.tool === 'question' || toolData?.type === 'question' ? '"Poppins", sans-serif' : 'Roboto',
                  }}
                >
                  {text}
                </Typography>
              )}
              <Box
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  width: '100%',
                  mt: ds.space[0],
                  gap: ds.space[2],
                  flexWrap: 'nowrap',
                }}
                key={toolData.messageId}
              >
                <Box sx={{ display: 'flex', alignItems: 'center' }}>
                  {(toolData.tool || toolData.type) === 'question' && (
                    <Box sx={{ flexShrink: 0 }}>
                      <CopyButton text={toolData.text} size='xs' />
                    </Box>
                  )}
                </Box>

                {(toolData.tool || toolData.type) === 'question' && (
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], justifyContent: 'flex-end', flexShrink: 0 }}>
                    {conversationCreatedAt && (
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], paddingLeft: ds.space[2] }}>
                        <SafeIcon src={WatchIcon} alt='conversation time' height={14} width={14} />
                        <Datetime
                          value={`${conversationCreatedAt}`}
                          sx={{ color: 'var(--ds-gray-400)' }}
                          sxSuffix={{ fontSize: 'var(--ds-text-small)' }}
                          sxSecondary={true}
                        />
                      </Box>
                    )}
                    {/* Chevron toggle removed — the inline "Show more / Show less" link in
                        MessageItem covers the same affordance more clearly. The whole-card
                        click-to-expand still works via the onClick on the outer Box. */}
                  </Box>
                )}
              </Box>
            </Box>
          </Grid>
          {(toolData.tool || toolData.type) !== 'question' && (
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'flex-end',
                gap: ds.space.mul(0, 3),
                textAlign: 'end',
                flexShrink: 0,
                '@media (max-width: 768px)': {
                  justifyContent: 'flex-start',
                  textAlign: 'start',
                },
              }}
            >
              {conversationCreatedAt && conversationUpdatedAt && !['planner', 'response'].includes(toolData.tool || toolData.type) && (
                <Duration createdAt={conversationCreatedAt} updatedAt={conversationUpdatedAt} />
              )}
              {headerActions}
            </Box>
          )}
        </Box>
      </Box>

      {(toolData.type === 'response' ||
        toolData.type === 'question' ||
        toolData.type === 'acknowledgment' ||
        toolData.type === 'followup-question') && <Box>{contentComponents}</Box>}
    </Box>
  );
}

ConversationCollapsableCard.propTypes = {
  id: PropTypes.string.isRequired,
  text: PropTypes.object.isRequired,
  contentComponents: PropTypes.node,
  toolData: PropTypes.object.isRequired,
  userName: PropTypes.string,
  conversationCreatedAt: PropTypes.string,
  conversationUpdatedAt: PropTypes.string,
  showFullTextHandler: PropTypes.func,
  textLength: PropTypes.bool,
  headerActions: PropTypes.node,
};

export default ConversationCollapsableCard;
