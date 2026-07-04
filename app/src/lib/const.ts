import { ds } from 'src/utils/colors';
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
    border: `0.3px solid ${ds.gray[600]}`,
    display: 'inline-flex',
    borderRadius: 'var(--ds-radius-sm)',
    alignItems: 'center',
    background: ds.background[100],
    fontSize: 'var(--ds-text-body-lg)',
    color: ds.gray[600],
    textTransform: 'unset',
    '&:hover': {
      background: ds.background[100],
    },
  },
  iconstyle: {
    fontSize: 'var(--ds-text-body-lg)',
  },
};
