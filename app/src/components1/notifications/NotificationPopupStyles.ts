import { colors } from 'src/utils/colors';

export const styles = {
  lightBlueLabel: {
    padding: '9px 16px',
    fontSize: '14px',
    fontWeight: 600,
    color: colors.text.tertiary,
    bgcolor: colors.background.primaryLightest,
    borderRadius: '4px',
    flexGrow: 1,
    mb: '16px',
  },

  numberWithHeading: {
    display: 'grid',
    gap: '8px',

    '& .main-heading': {
      padding: '9px 16px',
      fontSize: '14px',
      fontWeight: 600,
      color: colors.text.tertiary,
      bgcolor: colors.background.primaryLightest,
      borderRadius: '4px',
      flexGrow: 1,
      height: '40px',
      boxSizing: 'border-box',
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'space-between',
    },
  },
  grayLabel: {
    color: colors.text.tertiary,
    fontSize: '12px',
    fontWeight: '500',
  },
  tabButton: {
    width: '100%',
    padding: '8px 12px',
    fontSize: '14px',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    textTransform: 'unset',
    borderRadius: '4px',
    bgcolor: colors.background.primaryLightest,
    color: colors.text.secondary,
    fontWeight: '400',
    gap: '10px',

    '& img': {
      width: '14px',
      height: '14px',
      objectFit: 'contain',
    },

    '&.active': {
      bgcolor: colors.background.secondary,
      color: 'white',
      fontWeight: '500',
    },
  },
};
