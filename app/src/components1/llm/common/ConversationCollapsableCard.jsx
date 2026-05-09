import Datetime from '@common/format/Datetime';
import CopyableText from '@components1/common/CopyableText';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import { Avatar, Box, Button, Collapse, Grid, IconButton, Tooltip, Typography } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import PropTypes from 'prop-types';
import { IoIosArrowDown, IoIosArrowUp } from 'react-icons/io';
import { colors } from 'src/utils/colors';
import { getIcon } from './AgentIcon';
import { WatchIcon } from '@assets';

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
        overflow: 'hidden',
        borderRadius: (toolData.tool || toolData.type) === 'question' ? '12px' : '0px',
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
          padding: toolData.type != 'response' && toolData.type != 'question' ? `8px 10px 8px 16px` : '12px 16px 0px 16px',
          backgroundColor: toolData.type == 'question' ? '#F6F6F6' : colors.background.white,
          borderTop: collapsedObj[idx] ? `1.5px solid ${colors.border.primaryLightest}` : 'none',
        }}
      >
        <Box
          sx={{
            display: 'grid',
            alignItems: 'center',
            gridTemplateColumns: (toolData.tool || toolData.type) === 'question' ? '1fr' : '1fr 40px',
            gap: '24px',
            cursor: 'pointer',
            minHeight: '30px',
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
              gap: '12px',
            }}
          >
            {(toolData.tool || toolData.type) !== 'question' &&
              (toolData.tool || toolData.type) !== 'response' &&
              (getIcon(toolData.tool || toolData.type) ? (
                <Tooltip title={toolData.tool || toolData.type} placement='top'>
                  <SafeIcon
                    style={{
                      mixBlendMode: 'multiply',
                      width: (toolData.tool || toolData.type) === 'response' ? '26px' : '20px',
                      height: (toolData.tool || toolData.type) === 'response' ? '26px' : '20px',
                      objectFit: 'contain',
                      marginTop:
                        !(
                          toolData?.tool === 'question' ||
                          toolData?.tool === 'response' ||
                          toolData?.type === 'question' ||
                          toolData?.type === 'response'
                        ) && '0px',
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
                  <Avatar style={{ width: '24px', height: '24px' }}>{(toolData.tool[0] || toolData.type).toUpperCase()}</Avatar>
                </Tooltip>
              ))}
            <Box width={'100%'}>
              <Typography
                sx={{
                  fontSize: '15px',
                  color: colors.text.secondary,
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
                        <SafeIcon src={WatchIcon} alt='conversation time' height={14} />
                        <Datetime
                          value={`${conversationCreatedAt}`}
                          sx={{ color: colors.text.secondaryDark }}
                          sxSuffix={{ fontSize: '12px' }}
                          sxSecondary={true}
                        />
                      </Box>
                    )}
                    {textLength && (
                      <Button
                        sx={{ minWidth: '12px', padding: '0px', color: colors.text.tertiary, fontSize: '20px' }}
                        onClick={() => showFullTextHandler()}
                      >
                        {showFullText ? <IoIosArrowUp /> : <IoIosArrowDown />}
                      </Button>
                    )}
                  </Box>
                )}
              </Box>
            </Box>
          </Grid>
          {(toolData.tool || toolData.type) !== 'question' && (
            <Box display='flex' alignItems='center' justifyContent='flex-end' gap='8px' textAlign='end'>
              {toolData.type != 'response' && toolData.type != 'question' && toolData.type != 'acknowledgment' && (
                <IconButton onClick={(e) => handleIconClick(e)}>
                  <KeyboardArrowDownIcon
                    sx={{ transition: 'all ease 0.2s', transform: `rotate(${collapsedObj[idx] ? 180 : 0}deg)`, opacity: '50%', height: '20px' }}
                  />
                </IconButton>
              )}
            </Box>
          )}
        </Box>
      </Box>

      {toolData.type != 'response' && toolData.type != 'question' && toolData.type != 'acknowledgment' ? (
        <Collapse in={collapsedObj[idx]}>
          <Box
            sx={{
              maxHeight: collapsedObj[idx] ? '786px' : 'none',
              overflowY: collapsedObj[idx] ? 'auto' : 'hidden',
              padding: '0px 28px 28px 28px',
              backgroundColor: colors.background.white,
              borderBottom: collapsedObj[idx] ? `1.5px solid ${colors.border.primaryLightest}` : 'none',
              '&::-webkit-scrollbar': {
                width: '4px',
              },
            }}
          >
            {collapsedObj[idx] && contentComponents}
          </Box>
        </Collapse>
      ) : (
        <Box
          sx={{
            padding: '0px 16px 0px 16px',
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
