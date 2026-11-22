import { Box, Button, Typography } from '@mui/material';
import React from 'react';
import PropTypes from 'prop-types';
import { ds } from 'src/utils/colors';
import { PlusIconSecondary, ChatOutlineDarkIcon } from '@assets';
import NubiBrainNav from './NubiBrainNav';
import SafeIcon from '@shared/icons/SafeIcon';
import { NUBI_SUBNAV_WIDTH } from './railWidths';

const SubNavButton = ({ item, isFirstItem }) => (
  <Button
    sx={
      isFirstItem
        ? {
            '& > div:first-of-type': {
              padding: 'var(--ds-space-2)',
              border: `1px solid ${ds.blue[300]}`,
              borderRadius: 'var(--ds-radius-xl)',
              marginTop: 'var(--ds-space-3)',
            },
          }
        : undefined
    }
    aria-label={item.text ? undefined : item.ariaLabel}
    onClick={item.onClick}
  >
    <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 0 }}>
      <Box sx={styles.iconBox}>
        <SafeIcon
          priority
          src={item.icon}
          alt={item.text || item.ariaLabel || 'icon'}
          aria-label={item.text || item.ariaLabel}
          style={{ objectFit: 'contain', width: '22px', height: '22px' }}
          width={22}
          height={18}
        />
      </Box>
      {item.text && <Typography sx={styles.label}>{item.text}</Typography>}
    </Box>
  </Button>
);

SubNavButton.propTypes = {
  item: PropTypes.object.isRequired,
  isFirstItem: PropTypes.bool,
};

/**
 * Nubi's sub-navigation: a light column docked to the right of the app's main
 * rail on /ask-nudgebee. The main layout (components/common/layout/index.jsx)
 * owns everything app-wide — the rail, b-Cortex, the account menu, the tenant
 * switcher and the page title — so this only carries Nubi's own actions.
 */
const AskNudgebeeLayout = ({ children, handleNewChat, handleToggle }) => {
  const menuItems = [
    { icon: PlusIconSecondary, text: null, ariaLabel: 'New chat', onClick: handleNewChat },
    { icon: ChatOutlineDarkIcon, text: 'Chats', onClick: handleToggle },
  ];

  return (
    <Box sx={{ display: 'flex', alignItems: 'stretch', width: '100%' }}>
      <Box sx={styles.subNav} data-testid='nubi-subnav'>
        <Box className='inner-side-drawer'>
          <Box>
            {menuItems.map((item, idx) => (
              <SubNavButton key={item.text || item.ariaLabel} item={item} isFirstItem={idx === 0} />
            ))}
          </Box>
          <Box sx={styles.bottom}>
            {/* b-Cortex lives on the main rail only; this keeps AI & Tools. */}
            <NubiBrainNav surface='light' showBCortex={false} />
          </Box>
        </Box>
      </Box>

      <Box sx={styles.body}>{children}</Box>
    </Box>
  );
};

AskNudgebeeLayout.propTypes = {
  children: PropTypes.node.isRequired,
  handleNewChat: PropTypes.func,
  handleToggle: PropTypes.func,
};

const styles = {
  subNav: {
    width: NUBI_SUBNAV_WIDTH,
    flexShrink: 0,
    zIndex: 10,
    backgroundColor: ds.background[300],
    borderRight: `0.5px solid ${ds.gray[300]}`,
    '& .inner-side-drawer': {
      position: 'sticky',
      display: 'flex',
      flexDirection: 'column',
      alignItems: 'center',
      gap: 'var(--ds-space-1)',
      overflow: 'hidden',
      top: 0,
      height: '100vh',
    },
    '& button': {
      py: 'var(--ds-space-4)',
      width: ds.space.mul(1, 17),
      height: ds.space.mul(0, 35),
      display: 'flex',
      justifyContent: 'center',
      borderRadius: 0,
      '@media (max-width:1535px)': {
        py: 'var(--ds-space-2)',
        height: ds.space.mul(1, 13),
      },
      '&:hover': {
        backgroundColor: 'transparent',
      },
    },
  },
  iconBox: {
    width: ds.space.mul(0, 13),
    height: ds.space.mul(0, 13),
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    position: 'relative',
    '@media (max-width:1535px)': {
      width: ds.space.mul(0, 9),
      height: ds.space.mul(0, 9),
    },
  },
  label: {
    paddingTop: 'var(--ds-space-3)',
    lineHeight: ds.space[1],
    textTransform: 'capitalize',
    fontFamily: 'Roboto',
    fontWeight: 'var(--ds-font-weight-regular)',
    fontSize: 'var(--ds-text-caption)',
    color: ds.gray[600],
  },
  bottom: {
    marginTop: 'auto',
    paddingBottom: 'var(--ds-space-3)',
  },
  body: {
    flexGrow: 1,
    minWidth: 0,
    position: 'relative',
    display: 'flex',
    alignItems: 'center',
    flexDirection: 'column',
    backgroundColor: ds.background[100],
  },
};

export default AskNudgebeeLayout;
