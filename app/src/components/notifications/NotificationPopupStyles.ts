import { ds } from 'src/utils/colors';

export const styles = {
  lightBlueLabel: {
    padding: 'var(--ds-space-2) var(--ds-space-4)',
    fontSize: 'var(--ds-text-body-lg)',
    fontWeight: 'var(--ds-font-weight-semibold)',
    color: ds.gray[600],
    bgcolor: ds.blue[100],
    borderRadius: 'var(--ds-radius-sm)',
    flexGrow: 1,
    mb: 'var(--ds-space-4)',
  },

  numberWithHeading: {
    display: 'grid',
    gap: 'var(--ds-space-2)',

    '& .main-heading': {
      padding: 'var(--ds-space-2) var(--ds-space-4)',
      fontSize: 'var(--ds-text-body-lg)',
      fontWeight: 'var(--ds-font-weight-semibold)',
      color: ds.gray[600],
      bgcolor: ds.blue[100],
      borderRadius: 'var(--ds-radius-sm)',
      flexGrow: 1,
      height: ds.space.mul(1, 10),
      boxSizing: 'border-box',
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'space-between',
    },
  },
  grayLabel: {
    color: ds.gray[600],
    fontSize: 'var(--ds-text-small)',
    fontWeight: 'var(--ds-font-weight-medium)',
  },
  tabButton: {
    width: '100%',
    padding: 'var(--ds-space-2) var(--ds-space-3)',
    fontSize: 'var(--ds-text-body-lg)',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    textTransform: 'unset',
    borderRadius: 'var(--ds-radius-sm)',
    bgcolor: ds.blue[100],
    color: ds.gray[700],
    fontWeight: 'var(--ds-font-weight-regular)',
    gap: 'var(--ds-space-2)',

    '& img': {
      width: ds.space.mul(0, 7),
      height: ds.space.mul(0, 7),
      objectFit: 'contain',
    },

    '&.active': {
      bgcolor: ds.brand[500],
      color: 'white',
      fontWeight: 'var(--ds-font-weight-medium)',
    },
  },
};
