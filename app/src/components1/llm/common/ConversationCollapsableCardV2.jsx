import { WatchIcon } from '@assets';
import Datetime from '@common/format/Datetime';
import CopyableText from '@components1/common/CopyableText';
import { Avatar, Box, Grid, Tooltip, Typography } from '@mui/material';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';
import { getIcon } from './AgentIcon';
import SafeIcon from '@components1/common/SafeIcon';
import Duration from './Duration';

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
  let cardPadding = '12px 16px 12px 16px';
  if (toolData.type === 'response') {
    cardPadding = '4px 16px 8px 16px';
  } else if (toolData.type !== 'question') {
    cardPadding = '8px 10px 8px 8px';
  }

  return (
    <Box
      id={id}
      sx={{
        background: (toolData.tool || toolData.type) === 'question' ? colors.button.secondaryHover : colors.background.transparent,
        fontFamily: toolData.type === 'response' && '"Poppins", sans-serif',
        backgroundColor: (toolData.tool || toolData.type) === 'question' && colors.background.NubiQuestion,
        borderTop: 'none',
        borderBottom:
          (toolData.tool || toolData.type) === 'question'
            ? `0px`
            : (toolData.tool || toolData.type) === 'response'
            ? `none`
            : `0.8px solid ${colors.border.vertical}`,
        transition: 'all ease 0.2s',
        borderRadius: (toolData.tool || toolData.type) === 'question' ? '12px' : '0px',
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
          backgroundColor: toolData.type == 'question' ? '#F6F6F6' : colors.background.white,
          borderTop: 'none',
          borderRadius: '12px',
          cursor: (toolData.tool || toolData.type) === 'question' && textLength ? 'pointer' : 'default',
        }}
      >
        <Box
          sx={{
            display: 'grid',
            alignItems: 'center',
            gridTemplateColumns: (toolData.tool || toolData.type) === 'question' ? '1fr' : '1fr auto',
            gap: '24px',
            cursor: (toolData.tool || toolData.type) === 'question' && textLength ? 'pointer' : 'default',
            minHeight: '0px',
            boxSizing: 'border-box',
            backgroundColor:
              (toolData.tool || toolData.type) == 'question' || (toolData.tool || toolData.type) == 'followup-question' ? 'transparent' : '',
            width: 'auto',
            '@media (max-width: 768px)': {
              gridTemplateColumns: '1fr',
              gap: '8px',
            },
          }}
        >
          <Grid
            item
            md={5}
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: '12px',
            }}
          >
            {(toolData.tool || toolData.type) !== 'question' && (toolData.tool || toolData.type) !== 'response' && (
              <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '4px', flexShrink: 0 }}>
                {getIcon(toolData.tool || toolData.type) ? (
                  <Tooltip title={toolData.tool || toolData.type} placement='top'>
                    <SafeIcon
                      style={{
                        mixBlendMode: 'multiply',
                        width: '20px',
                        height: '20px',
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
                    <Avatar style={{ width: '24px', height: '24px' }}>{(toolData.tool?.[0] || toolData.type).toUpperCase()}</Avatar>
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
                    fontSize: '15px',
                    color: colors.text.secondary,
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
                  mt: '2px',
                  gap: '8px',
                  flexWrap: 'nowrap',
                }}
                key={toolData.messageId}
              >
                <Box sx={{ display: 'flex', alignItems: 'center' }}>
                  {(toolData.tool || toolData.type) === 'question' && (
                    <Box sx={{ flexShrink: 0 }}>
                      <CopyableText copyableText={toolData.text} iconColor={undefined} />
                    </Box>
                  )}
                </Box>

                {(toolData.tool || toolData.type) === 'question' && (
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px', justifyContent: 'flex-end', flexShrink: 0 }}>
                    {conversationCreatedAt && (
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px', paddingLeft: '8px' }}>
                        <SafeIcon src={WatchIcon} alt='conversation time' height={14} width={14} />
                        <Datetime
                          value={`${conversationCreatedAt}`}
                          sx={{ color: colors.text.secondaryDark }}
                          sxSuffix={{ fontSize: '12px' }}
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
                gap: '6px',
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
