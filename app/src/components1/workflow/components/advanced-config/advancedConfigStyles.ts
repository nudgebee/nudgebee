import { colors } from 'src/utils/colors';

// Shared styles for code/monospace input fields
export const codeInputStyles = {
  '& .MuiInputBase-root': {
    bgcolor: 'var(--ds-background-200)',
    '&:hover': {
      bgcolor: 'var(--ds-background-300)',
    },
    '&.Mui-focused': {
      bgcolor: 'var(--ds-background-100)',
    },
  },
  '& .MuiInputBase-input': {
    fontFamily: 'monospace',
    fontSize: 'var(--ds-text-small)',
    resize: 'vertical' as const,
  },
};

// Styles for JSON/code textarea fields
export const jsonTextareaStyles = {
  ...codeInputStyles,
  '& .MuiInputBase-input': {
    fontFamily: 'monospace',
    fontSize: 'var(--ds-text-small)',
    resize: 'vertical' as const,
    lineHeight: 1.5,
  },
};

// Empty state styles
export const emptyStateStyles = {
  container: {
    display: 'flex',
    flexDirection: 'column' as const,
    alignItems: 'center',
    justifyContent: 'center',
    py: 3,
    px: 2,
    border: `2px dashed ${colors.lowestLight}`,
    borderRadius: 1,
    bgcolor: 'var(--ds-background-200)',
  },
  icon: {
    fontSize: 32,
    color: colors.text.secondary,
    opacity: 0.4,
    mb: 1,
  },
  text: {
    fontSize: 'var(--ds-text-small)',
    color: colors.text.secondary,
    textAlign: 'center' as const,
    mb: 1.5,
  },
  button: {
    fontSize: 'var(--ds-text-small)',
    textTransform: 'none' as const,
  },
};

// View toggle button styles
export const viewToggleStyles = (isActive: boolean) => ({
  p: 0.5,
  bgcolor: isActive ? 'primary.light' : 'transparent',
  color: isActive ? 'primary.contrastText' : colors.text.secondary,
  '&:hover': {
    bgcolor: isActive ? 'primary.light' : 'action.hover',
  },
});

// Preset chip styles
export const presetChipStyles = (isSelected: boolean) => ({
  fontSize: 'var(--ds-text-caption)',
  height: 20,
  bgcolor: isSelected ? 'primary.light' : colors.lowestLight,
  color: isSelected ? 'primary.contrastText' : colors.text.secondary,
  '&:hover': {
    bgcolor: isSelected ? 'primary.main' : colors.background.tertiaryLightest,
  },
});
