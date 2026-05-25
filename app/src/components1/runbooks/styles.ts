/**
 * @deprecated Runbook functionality has been replaced by Workflows.
 * This file is kept for backward compatibility with existing executions.
 * TODO: Remove once workflow migration is complete.
 */
import { colors } from 'src/utils/colors';

export const styles = {
  lightBlueLabel: {
    padding: '9px 16px',
    fontSize: '14px',
    fontWeight: 600,
    color: colors.text.secondary,
    bgcolor: colors.background.primaryLightest,
    borderRadius: '4px',
    flexGrow: 1,
    mb: '16px',
  },

  numberWithHeading: {
    display: 'grid',
    gridTemplateColumns: '40px 1fr',
    gap: '8px',

    '& .number-heading': {
      height: '40px',
      width: '40px',
      bgcolor: colors.background.primaryLight,
      borderRadius: '4px',
      fontSize: '16px',
      fontWeight: '600',
      color: colors.text.secondary,
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
    },

    '& .main-heading': {
      padding: '9px 16px',
      fontSize: '14px',
      fontWeight: 600,
      color: colors.text.secondary,
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
    width: '180px',
    minWidth: 'fit-content',
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
      bgcolor: colors.text.secondary,
      color: 'white',
      fontWeight: '500',
    },
  },
  radioButtonsGroup: {
    fontFamily: 'inherit',
    '& .MuiFormControlLabel-label ': { fontSize: '16px', fontFamily: 'inherit', fontWeight: 400, color: colors.text.secondary, mr: '40px' },
    '& .MuiRadio-root': {
      p: '8px',
      '& svg': { width: '16px', height: '16px' },
    },
  },
  radioButtonsGroupSmall: {
    fontFamily: 'inherit',
    '& .MuiFormControlLabel-label ': { fontSize: '14px', fontFamily: 'inherit', fontWeight: 500, color: colors.text.secondary, mr: '40px' },
    '& .MuiRadio-root': {
      p: '8px',
      '& svg': { width: '16px', height: '16px' },
    },
  },
  grid: {
    display: 'grid',
    gap: '10px',
    gridTemplateColumns: '1fr 36px',
  },
  accordion: {
    border: 'none',
    boxShadow: 'none',
    '& .MuiAccordionSummary-root': {
      bgcolor: colors.background.accordionSummay,
      fontSize: '12px',
      fontWeight: '500',
      color: colors.text.secondary,
      padding: '9px 16px',
      minHeight: 'unset',
      borderRadius: '4px',
      border: `0.5px solid ${colors.border.errorLight}`,

      '&.Mui-expanded': {
        minHeight: 'unset',
        borderRadius: '4px 4px 0px 0px',
      },

      '& .MuiAccordionSummary-content': {
        margin: '0px',
        padding: '0px',
      },
    },

    '&.gray-accordion': {
      '& .MuiAccordionSummary-root': {
        color: colors.text.tertiary,
        bgcolor: colors.background.tertiaryLightest,
        border: `0.5px solid ${colors.border.tertiaryLightest}`,
      },
    },

    '& .MuiAccordionDetails-root': {
      padding: '12px 24px',
      minHeight: 'unset',
      borderRadius: '0 0 4px 4px',
      border: `0.5px solid ${colors.border.errorLight}`,
      borderTop: 'none',
      color: colors.text.tertiary,
      fontSize: '14px',
    },
  },
};
