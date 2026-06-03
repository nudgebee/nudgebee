import React, { useState } from 'react';
import Typography from '@mui/material/Typography';
import { IconButton, Box, Grid, Avatar, Collapse, ListItem } from '@mui/material';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import HighLights from './HighLights';
import CustomButton from '@common/NewCustomButton';
import SparklesIcon from '@assets/kubernetes/sparkle.svg';
import { SearchOutlined } from '@mui/icons-material';
import { WrenchIconOutline, BetaIcon } from '@assets';
import { hasWriteAccess } from '@lib/auth';
import { useRouter } from 'next/router';
import SafeIcon from '@common/SafeIcon';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import CustomBorderCard from '@components1/ds/CustomBorderCard';
import { colors } from 'src/utils/colors';

function CollapsableCard({
  id,
  icon,
  text,
  highlightsData = [],
  contentComponents,
  idx,
  onCardClick,
  expandedCardIndex,
  collapsedObj,
  resolveButton,
  resolveButtonClick,
  ResolveComponent,
  isBeta = false,
  disabled = false,
  maxWidth = '1500px',
  eventResolution = null,
}) {
  const [openResolveComponent, setOpenResolveComponent] = useState(false);
  const [showAll, setShowAll] = useState(false);

  const MAX_VISIBLE = 1;
  const visibleItems = showAll ? highlightsData : highlightsData.slice(0, MAX_VISIBLE);
  const router = useRouter();
  const { accountId } = router.query;

  const getResolutionDescription = (resolution) => {
    if (!resolution?.data) return '';
    const d = typeof resolution.data === 'string' ? JSON.parse(resolution.data) : resolution.data;
    const input = d?.data;
    if (!input) return '';

    // Container resource changes (cpu/memory resize)
    if (input.container_name && input[input.container_name]) {
      const containerData = input[input.container_name];
      const meta = input.cloud_resourse?.meta;
      const lines = [];
      if (meta) {
        const parts = [input.container_name];
        if (meta.controllerKind && meta.controller) parts.push(`${meta.controllerKind}/${meta.controller}`);
        if (meta.namespace) parts.push(meta.namespace);
        lines.push(parts.join(' · '));
      }
      if (containerData.memory) {
        if (containerData.memory.oldLimit && containerData.memory.limit != null)
          lines.push(`Memory Limit: ${containerData.memory.oldLimit} → ${containerData.memory.limit}`);
        if (containerData.memory.oldRequest && containerData.memory.request != null)
          lines.push(`Memory Request: ${containerData.memory.oldRequest} → ${containerData.memory.request}`);
      }
      if (containerData.cpu) {
        if (containerData.cpu.oldLimit && containerData.cpu.limit != null)
          lines.push(`CPU Limit: ${containerData.cpu.oldLimit} → ${containerData.cpu.limit}`);
        if (containerData.cpu.oldRequest != null && containerData.cpu.request != null)
          lines.push(`CPU Request: ${containerData.cpu.oldRequest} → ${containerData.cpu.request}`);
      }
      return lines.join('\n');
    }

    if (input.size) return `Resize to ${input.size}`;
    if (input.increase_replicas) return `Increase replicas to ${input.increase_replicas}`;
    if (input.restart) return 'Restart pod';
    if (input.revert) return 'Revert deployment';
    if (input.imageNameWithTag) return `Change image to ${input.imageNameWithTag}`;
    if (input.cordon) return 'Cordon node';
    if (input.drain) return 'Drain node';
    return '';
  };

  const handleCollapse = () => {
    // If expanded then true else false
    onCardClick(idx);
  };

  const isExpanded = expandedCardIndex == idx;

  const closeResolveComponent = () => {
    setOpenResolveComponent(false);
  };

  return (
    <Box
      id={id}
      sx={{
        background: colors.background.white,
        borderBottom: collapsedObj[idx] ? `2px solid ${colors.text.primaryLight}` : '0.5px solid rgba(225, 225, 225, 0.44)',
        borderTop: collapsedObj[idx] ? `2px solid ${colors.text.primaryLight}` : 'none',
        transition: 'all ease 0.2s',
        marginBottom: '0.5px',
        opacity: 0,
        transform: 'translateY(8px)',
        animation: `fadeInUp 0.5s ease forwards`,
        '@keyframes fadeInUp': {
          '0%': {
            opacity: 0,
            transform: 'translateY(8px)',
          },
          '100%': {
            opacity: 1,
            transform: 'translateY(0)',
          },
        },
      }}
    >
      {ResolveComponent && <ResolveComponent open={openResolveComponent} onCloseComponent={closeResolveComponent} />}
      <Box sx={{ display: 'flex', flexDirection: 'column' }}>
        <Box
          sx={{
            display: 'grid',
            alignItems: 'center',
            gridTemplateColumns: { xs: '1fr', sm: '1fr 300px 85px', md: '1fr 459px 85px' },
            gap: 'var(--ds-space-5)',
            cursor: 'pointer',
            minHeight: '58px',
            padding: 'var(--ds-space-2) var(--ds-space-2)',
            boxSizing: 'border-box',
            backgroundColor: disabled ? colors.text.disabled : colors.background.white,
            overflow: 'hidden',
            '@media (max-width: 1400px)': {
              gridTemplateColumns: '1fr 360px 100px',
            },
            '@media (max-width: 1220px)': {
              gridTemplateColumns: '1fr 220px 100px',
            },
            '@media (max-width: 1100px)': {
              gridTemplateColumns: '1fr 160px 100px',
            },
          }}
          onClick={() => handleCollapse()}
        >
          <Grid item md={5} sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', minWidth: 0 }}>
            <SafeIcon
              style={{ mixBlendMode: 'multiply', width: '22px', height: '22px', objectFit: 'contain' }}
              src={icon}
              alt='Your Image'
              className='icon'
            />
            <Typography
              sx={{
                fontFamily: 'Poppins',
                fontWeight: 'var(--ds-font-weight-medium)',
                fontSize: 'var(--ds-text-small)',
                color: colors.secondary.default,
              }}
              variant='body1'
            >
              {text}
            </Typography>
            {isBeta && <SafeIcon src={BetaIcon} alt='beta-icon' priority={true} />}
          </Grid>

          {highlightsData.length > 0 ? (
            <Grid
              sx={{
                display: 'flex',
                flexDirection: 'column',
                gap: 'var(--ds-space-1)',
                minWidth: 0, // allow child text to shrink and show ellipsis
              }}
            >
              {visibleItems?.map((hobj, index) => (
                <Box key={index} sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)', minWidth: 0 }}>
                  <Avatar sx={{ width: '16px', height: '16px', bgcolor: 'transparent' }}>
                    <SafeIcon src={SparklesIcon} alt='start-icon' priority={true} />
                  </Avatar>
                  <HighLights
                    key={index}
                    text={hobj.message || hobj}
                    component={hobj.component}
                    containerStyles={{ padding: 0, flex: 1, minWidth: 0, overflow: 'hidden' }}
                    styles={{
                      color: hobj?.severity === 'Critical' ? '#EF4444' : '#374151',
                      fontSize: 'var(--ds-text-small)',
                      fontWeight: 'var(--ds-font-weight-regular)',
                      fontStyle: 'italic',
                      wordBreak: 'break-all',
                    }}
                  />
                </Box>
              ))}
              {highlightsData?.length > MAX_VISIBLE && (
                <ListItem
                  alignItems='center'
                  sx={{ p: '0px 0px var(--ds-space-1) var(--ds-space-4)', cursor: 'pointer' }}
                  onClick={(e) => {
                    e.stopPropagation();
                    setShowAll(!showAll);
                  }}
                >
                  <Typography
                    sx={{
                      color: colors.primary,
                      fontSize: 'var(--ds-text-small)',
                    }}
                  >
                    {showAll ? 'Show less' : `Show more (${highlightsData?.length - MAX_VISIBLE})`}
                  </Typography>
                </ListItem>
              )}
            </Grid>
          ) : (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-1)', minWidth: 0 }}>
              <SearchOutlined style={{ width: '16px', height: '16px', color: colors.text.tertiarymedium }} />
              <HighLights
                text={'Nothing major to show. Check the card for more.'}
                containerStyles={{ padding: 0, flex: 1, minWidth: 0, overflow: 'hidden' }}
                styles={{
                  color: colors.text.tertiarymedium,
                  fontSize: 'var(--ds-text-small)',
                  fontStyle: 'italic',
                  wordBreak: 'break-all',
                }}
              />
            </Box>
          )}
          <Box display='flex' alignItems='center' justifyContent='flex-end' gap={'8px'} textAlign={'end'}>
            {resolveButton && eventResolution ? (
              <CustomLabels text={eventResolution.status === 'InProgress' ? 'In Progress' : eventResolution.status} height='24px' />
            ) : resolveButton && hasWriteAccess(accountId) ? (
              <CustomButton
                variant='tertiary'
                sx={{ width: 'max-content', whiteSpace: 'nowrap' }}
                size='xSmall'
                startIcon={
                  <Box component='span' sx={{ display: 'inline-flex', width: 12, height: 12, flexShrink: 0 }}>
                    <SafeIcon src={WrenchIconOutline} alt='start-icon' priority={true} />
                  </Box>
                }
                text={'Fix it'}
                onClick={(e) => {
                  if (e) {
                    e.stopPropagation();
                  }
                  if (resolveButtonClick) {
                    resolveButtonClick(id);
                  } else if (ResolveComponent) {
                    setOpenResolveComponent(true);
                  }
                }}
              />
            ) : null}
            <IconButton
              onClick={(e) => {
                e.stopPropagation();
                handleCollapse();
              }}
            >
              <KeyboardArrowDownIcon sx={{ transition: 'all ease 0.s', transform: `rotate(${collapsedObj[idx] ? 180 : 0}deg)`, opacity: 0.5 }} />
            </IconButton>
          </Box>
        </Box>
      </Box>
      <Collapse in={collapsedObj[idx]}>
        <Box
          sx={{
            maxHeight: collapsedObj[idx] ? '1200px' : 'none', // Set maximum height when collapsed
            overflowY: collapsedObj[idx] ? 'auto' : 'hidden', // Hide overflow or make it scrollable
            borderTop: `0.5px solid ${colors.border.secondaryLight}`,
            padding: 'var(--ds-space-2) var(--ds-space-5) var(--ds-space-5) var(--ds-space-5)',
            maxWidth: maxWidth,
            overflow: 'auto !important',
          }}
        >
          {isExpanded && eventResolution && (
            <CustomBorderCard
              borderLeftColor={eventResolution.status === 'Failed' ? colors.highest : colors.text.primaryLight}
              borderLeftWidth='3px'
              padding='10px 16px'
              sx={{ mb: 'var(--ds-space-3)', bgcolor: colors.background.suggestionCardBG }}
            >
              <Box sx={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 'var(--ds-space-2)' }}>
                <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--ds-space-2)' }}>
                  <CustomLabels text={eventResolution.status === 'InProgress' ? 'In Progress' : eventResolution.status} height='22px' />
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
                    {getResolutionDescription(eventResolution)
                      .split('\n')
                      .filter(Boolean)
                      .map((line, i) => (
                        <Typography key={i} sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)' }}>
                          {line}
                        </Typography>
                      ))}
                    {eventResolution.status === 'Failed' && eventResolution.status_message && (
                      <Typography sx={{ fontSize: 'var(--ds-text-small)', color: colors.highest, mt: 'var(--ds-space-1)' }}>
                        {eventResolution.status_message}
                      </Typography>
                    )}
                  </Box>
                </Box>
                {eventResolution.status === 'Failed' && resolveButton && hasWriteAccess(accountId) && (
                  <CustomButton
                    variant='tertiary'
                    sx={{ width: 'max-content', whiteSpace: 'nowrap', flexShrink: 0 }}
                    size='xSmall'
                    startIcon={<SafeIcon src={WrenchIconOutline} alt='retry-icon' priority={true} />}
                    text={'Retry'}
                    onClick={() => {
                      if (resolveButtonClick) {
                        resolveButtonClick(id);
                      } else if (ResolveComponent) {
                        setOpenResolveComponent(true);
                      }
                    }}
                  />
                )}
              </Box>
            </CustomBorderCard>
          )}
          {isExpanded && contentComponents.map((Component, index) => <Component key={index} />)}
        </Box>
      </Collapse>
    </Box>
  );
}

export default React.memo(CollapsableCard, (prevProps, nextProps) => {
  // Prevent re-render if this card's expanded/collapsed state and resolution haven't changed.
  // Compare per-card isExpanded instead of global expandedCardIndex to avoid all cards
  // re-rendering when a single card is toggled.
  const prevIsExpanded = prevProps.expandedCardIndex === prevProps.idx;
  const nextIsExpanded = nextProps.expandedCardIndex === nextProps.idx;
  return (
    prevIsExpanded === nextIsExpanded &&
    prevProps.collapsedObj[prevProps.idx] === nextProps.collapsedObj[nextProps.idx] &&
    prevProps.eventResolution === nextProps.eventResolution
  );
});
