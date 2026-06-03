import Datetime from '@common/format/Datetime';
import Tooltip from '@components1/ds/Tooltip';
import CopyButton from '@common-new/CopyButton';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import { Avatar, Box, Collapse, Grid, Typography } from '@mui/material';
import { Button } from '@components1/ds/Button';
import SafeIcon from '@components1/common/SafeIcon';
import PropTypes from 'prop-types';
import { IoIosArrowDown, IoIosArrowUp } from 'react-icons/io';
import { getIcon } from './AgentIcon';
import { WatchIcon } from '@assets';
import { ds } from '@utils/colors';

function ConversationCollapsableCard({
  id,
  text,
  contentComponents,
  idx,
  onCardClick,
  collapsedObj,
  toolData,
  conversationCreatedAt = '',
  showFullTextHandler = () => {
    // Removed console.log statement
  },
  showFullText = false,
  textLength = false,
}) {
  const handleCollapse = () => {
    if (toolData.type != 'response' && toolData.type != 'question') {
      onCardClick(idx);
    }
  };

  const handleIconClick = (event) => {
    event.stopPropagation();
    handleCollapse();
  };

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
        overflow: 'hidden',
        borderRadius: (toolData.tool || toolData.type) === 'question' ? ds.radius.xl : 0,
        animation: `unblurAndScale 0.6s cubic-bezier(0.215, 0.61, 0.355, 1) both 0s`,
        '@keyframes unblurAndScale': {
          '0%': {
            transform: 'translateY(80px) scale(0.9)',
            filter: 'blur(5px)',
            opacity: 0,
          },
          '85%': {
            filter: 'blur(0px)',
          },
          '100%': {
            transform: 'translateY(0px) scale(1)',
            filter: 'blur(0px)',
            opacity: 1,
          },
        },
      }}
    >
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
          padding:
            toolData.type != 'response' && toolData.type != 'question'
              ? `${ds.space[2]} ${ds.space.mul(0, 5)} ${ds.space[2]} ${ds.space[4]}`
              : `${ds.space[3]} ${ds.space[4]} 0 ${ds.space[4]}`,
          backgroundColor: toolData.type == 'question' ? '#F6F6F6' : 'var(--ds-background-100)',
          borderTop: collapsedObj[idx] ? `1.5px solid ${'var(--ds-blue-400)'}` : 'none',
        }}
      >
        <Box
          sx={{
            display: 'grid',
            alignItems: 'center',
            gridTemplateColumns: (toolData.tool || toolData.type) === 'question' ? '1fr' : '1fr 40px',
            gap: ds.space[5],
            cursor: 'pointer',
            minHeight: ds.space.mul(0, 15),
            boxSizing: 'border-box',
            backgroundColor:
              (toolData.tool || toolData.type) == 'question' || (toolData.tool || toolData.type) == 'followup-question' ? 'transparent' : '',
            width: 'auto',
          }}
          onClick={() => handleCollapse()}
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
            {(toolData.tool || toolData.type) !== 'question' &&
              (toolData.tool || toolData.type) !== 'response' &&
              (getIcon(toolData.tool || toolData.type) ? (
                <Tooltip title={toolData.tool || toolData.type} placement='top'>
                  <SafeIcon
                    style={{
                      mixBlendMode: 'multiply',
                      width: (toolData.tool || toolData.type) === 'response' ? ds.space.mul(0, 13) : ds.space.mul(1, 5),
                      height: (toolData.tool || toolData.type) === 'response' ? ds.space.mul(0, 13) : ds.space.mul(1, 5),
                      objectFit: 'contain',
                      marginTop:
                        !(
                          toolData?.tool === 'question' ||
                          toolData?.tool === 'response' ||
                          toolData?.type === 'question' ||
                          toolData?.type === 'response'
                        ) && 0,
                    }}
                    src={getIcon(toolData.tool || toolData.type)}
                    alt={toolData.tool || toolData.type}
                    className='icon'
                    width={24}
                    height={24}
                  />
                </Tooltip>
              ) : (
                <Tooltip title={toolData.tool[0] || toolData.type} placement='top'>
                  <Avatar style={{ width: ds.space[5], height: ds.space[5] }}>{(toolData.tool[0] || toolData.type).toUpperCase()}</Avatar>
                </Tooltip>
              ))}
            <Box width={'100%'}>
              <Typography
                sx={{
                  fontSize: 'var(--ds-text-body-lg)',
                  color: 'var(--ds-gray-700)',
                  fontFamily: toolData?.tool === 'question' || toolData?.type === 'question' ? '"Poppins", sans-serif' : 'Roboto',
                }}
              >
                {text}
              </Typography>
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
                        <SafeIcon src={WatchIcon} alt='conversation time' height={14} />
                        <Datetime
                          value={`${conversationCreatedAt}`}
                          sx={{ color: 'var(--ds-gray-400)' }}
                          sxSuffix={{ fontSize: 'var(--ds-text-small)' }}
                          sxSecondary={true}
                        />
                      </Box>
                    )}
                    {textLength && (
                      <Button
                        tone='ghost'
                        size='xs'
                        composition='icon-only'
                        aria-label={showFullText ? 'Show less' : 'Show more'}
                        icon={showFullText ? <IoIosArrowUp /> : <IoIosArrowDown />}
                        onClick={() => showFullTextHandler()}
                      />
                    )}
                  </Box>
                )}
              </Box>
            </Box>
          </Grid>
          {(toolData.tool || toolData.type) !== 'question' && (
            <Box display='flex' alignItems='center' justifyContent='flex-end' gap={ds.space[2]} textAlign='end'>
              {toolData.type != 'response' && toolData.type != 'question' && toolData.type != 'acknowledgment' && (
                <Button
                  tone='ghost'
                  size='sm'
                  icon={
                    <KeyboardArrowDownIcon
                      sx={{
                        transition: 'all ease 0.2s',
                        transform: `rotate(${collapsedObj[idx] ? 180 : 0}deg)`,
                        opacity: '50%',
                        height: ds.space.mul(1, 5),
                      }}
                    />
                  }
                  onClick={(e) => handleIconClick(e)}
                  aria-label='Toggle collapse'
                />
              )}
            </Box>
          )}
        </Box>
      </Box>

      {toolData.type != 'response' && toolData.type != 'question' && toolData.type != 'acknowledgment' ? (
        <Collapse in={collapsedObj[idx]}>
          <Box
            sx={{
              maxHeight: collapsedObj[idx] ? ds.space.mul(0, 393) : 'none',
              overflowY: collapsedObj[idx] ? 'auto' : 'hidden',
              padding: `0 ${ds.space.mul(1, 7)} ${ds.space.mul(1, 7)} ${ds.space.mul(1, 7)}`,
              backgroundColor: 'var(--ds-background-100)',
              borderBottom: collapsedObj[idx] ? `1.5px solid ${'var(--ds-blue-400)'}` : 'none',
              '&::-webkit-scrollbar': {
                width: ds.space[1],
              },
            }}
          >
            {collapsedObj[idx] && contentComponents}
          </Box>
        </Collapse>
      ) : (
        <Box
          sx={{
            padding: `0 ${ds.space[4]} 0 ${ds.space[4]}`,
          }}
        >
          {contentComponents}
        </Box>
      )}
    </Box>
  );
}

ConversationCollapsableCard.propTypes = {
  id: PropTypes.string.isRequired,
  text: PropTypes.object.isRequired,
  contentComponents: PropTypes.node.isRequired,
  idx: PropTypes.number.isRequired,
  onCardClick: PropTypes.func.isRequired,
  collapsedObj: PropTypes.object.isRequired,
  toolData: PropTypes.object.isRequired,
  userName: PropTypes.string,
  conversationCreatedAt: PropTypes.string,
  showFullTextHandler: PropTypes.func,
  showFullText: PropTypes.bool,
  textLength: PropTypes.bool,
  hideIcon: PropTypes.string,
};

export default ConversationCollapsableCard;
