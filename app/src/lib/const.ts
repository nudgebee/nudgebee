import { colors } from 'src/utils/colors';
export const TableHeadStyle = { fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-semibold)' };
export const TableContentStyle = { fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-regular)' };
export const DownloadShareStyle = {
  tabStyle: {
    textTransform: 'capitalize',
    fontWeight: 'var(--ds-font-weight-semibold)',
  },
  buttonstyle: {
    // border: '1px solid var(--ds-brand-200) !important',
    padding: 'var(--ds-space-1) var(--ds-space-2)',
    marginLeft: 'var(--ds-space-1)',
    border: `0.3px solid ${colors.tertiary}`,
    display: 'inline-flex',
    borderRadius: 'var(--ds-radius-sm)',
    alignItems: 'center',
    background: colors.white,
    fontSize: 'var(--ds-text-body-lg)',
    color: colors.tertiary,
    textTransform: 'unset',
    '&:hover': {
      background: colors.white,
    },
  },
  iconstyle: {
    fontSize: 'var(--ds-text-body-lg)',
  },
};
