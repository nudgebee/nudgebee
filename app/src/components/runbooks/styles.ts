/**
 * @deprecated Runbook functionality has been replaced by Workflows.
 * This file is kept for backward compatibility with existing executions.
 * TODO: Remove once workflow migration is complete.
 */
import { ds } from 'src/utils/colors';

export const styles = {
  lightBlueLabel: {
    padding: 'var(--ds-space-2) var(--ds-space-4)',
    fontSize: 'var(--ds-text-body-lg)',
    fontWeight: 'var(--ds-font-weight-semibold)',
    color: ds.brand[500],
    bgcolor: ds.blue[100],
    borderRadius: 'var(--ds-radius-sm)',
    flexGrow: 1,
    mb: 'var(--ds-space-4)',
  },

  numberWithHeading: {
    display: 'grid',
    gridTemplateColumns: '40px 1fr',
    gap: 'var(--ds-space-2)',

    '& .number-heading': {
      height: ds.space.mul(0, 20),
      width: ds.space.mul(0, 20),
      bgcolor: ds.blue[300],
      borderRadius: 'var(--ds-radius-sm)',
      fontSize: 'var(--ds-text-title)',
      fontWeight: 'var(--ds-font-weight-semibold)',
      color: ds.brand[500],
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
    },

    '& .main-heading': {
      padding: 'var(--ds-space-2) var(--ds-space-4)',
      fontSize: 'var(--ds-text-body-lg)',
      fontWeight: 'var(--ds-font-weight-semibold)',
      color: ds.brand[500],
      bgcolor: ds.blue[100],
      borderRadius: 'var(--ds-radius-sm)',
      flexGrow: 1,
      height: ds.space.mul(0, 20),
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
    width: ds.space.mul(0, 90),
    minWidth: 'fit-content',
    padding: 'var(--ds-space-2) var(--ds-space-3)',
    fontSize: 'var(--ds-text-body-lg)',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    textTransform: 'unset',
    borderRadius: 'var(--ds-radius-sm)',
    bgcolor: ds.blue[100],
    color: ds.brand[500],
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
  radioButtonsGroup: {
    fontFamily: 'inherit',
    '& .MuiFormControlLabel-label ': {
      fontSize: 'var(--ds-text-title)',
      fontFamily: 'inherit',
      fontWeight: 'var(--ds-font-weight-regular)',
      color: ds.brand[500],
      mr: 'var(--ds-space-6)',
    },
    '& .MuiRadio-root': {
      p: 'var(--ds-space-2)',
      '& svg': { width: ds.space[4], height: ds.space[4] },
    },
  },
  radioButtonsGroupSmall: {
    fontFamily: 'inherit',
    '& .MuiFormControlLabel-label ': {
      fontSize: 'var(--ds-text-body-lg)',
      fontFamily: 'inherit',
      fontWeight: 'var(--ds-font-weight-medium)',
      color: ds.brand[500],
      mr: 'var(--ds-space-6)',
    },
    '& .MuiRadio-root': {
      p: 'var(--ds-space-2)',
      '& svg': { width: ds.space[4], height: ds.space[4] },
    },
  },
  grid: {
    display: 'grid',
    gap: 'var(--ds-space-2)',
    gridTemplateColumns: '1fr 36px',
  },
  accordion: {
    border: 'none',
    boxShadow: 'none',
    '& .MuiAccordionSummary-root': {
      bgcolor: ds.red[100],
      fontSize: 'var(--ds-text-small)',
      fontWeight: 'var(--ds-font-weight-medium)',
      color: ds.brand[500],
      padding: 'var(--ds-space-2) var(--ds-space-4)',
      minHeight: 'unset',
      borderRadius: 'var(--ds-radius-sm)',
      border: `0.5px solid ${ds.red[200]}`,

      '&.Mui-expanded': {
        minHeight: 'unset',
        borderRadius: 'var(--ds-radius-sm) var(--ds-radius-sm) 0px 0px',
      },

      '& .MuiAccordionSummary-content': {
        margin: '0px',
        padding: '0px',
      },
    },

    '&.gray-accordion': {
      '& .MuiAccordionSummary-root': {
        color: ds.gray[600],
        bgcolor: ds.gray[100],
        border: `0.5px solid ${ds.gray[100]}`,
      },
    },

    '& .MuiAccordionDetails-root': {
      padding: 'var(--ds-space-3) var(--ds-space-5)',
      minHeight: 'unset',
      borderRadius: '0 0 var(--ds-radius-sm) var(--ds-radius-sm)',
      border: `0.5px solid ${ds.red[200]}`,
      borderTop: 'none',
      color: ds.gray[600],
      fontSize: 'var(--ds-text-body-lg)',
    },
  },
};
