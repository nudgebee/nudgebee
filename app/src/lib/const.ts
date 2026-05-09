import { colors } from 'src/utils/colors';
export const TableHeadStyle = { fontSize: '16px', fontWeight: 600 };
export const TableContentStyle = { fontSize: '14px', fontWeight: 400 };
export const DownloadShareStyle = {
  tabStyle: {
    textTransform: 'capitalize',
    fontWeight: '600',
  },
  buttonstyle: {
    // border: '1px solid #CBD5E1 !important',
    padding: '5px 7px',
    marginLeft: '3px',
    border: `0.3px solid ${colors.tertiary}`,
    display: 'inline-flex',
    borderRadius: '4px',
    alignItems: 'center',
    background: colors.white,
    fontSize: '14px',
    color: colors.tertiary,
    textTransform: 'unset',
    '&:hover': {
      background: colors.white,
    },
  },
  iconstyle: {
    fontSize: '15px',
  },
};
