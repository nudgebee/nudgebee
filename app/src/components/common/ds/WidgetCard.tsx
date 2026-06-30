import { Box, BoxProps } from '@mui/material';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';

/**
 * WidgetCard - A reusable white card/widget component with consistent styling
 * Used across the application for displaying content in elevated white containers
 */
const WidgetCard = ({ children, sx = {}, ...props }: BoxProps) => {
  return (
    <Box
      sx={{
        border: `1px solid ${colors.border.secondaryLight}`,
        backgroundColor: colors.background.white,
        boxShadow: colors.shadow.card,
        padding: 'var(--ds-space-4) var(--ds-space-5)',
        borderRadius: 'var(--ds-radius-xl)',
        mt: 'var(--ds-space-5)',
        '@media(max-width: 1170px)': {
          padding: 'var(--ds-space-4) !important',
        },
        ...sx,
      }}
      {...props}
    >
      {children}
    </Box>
  );
};

WidgetCard.propTypes = {
  children: PropTypes.node,
  sx: PropTypes.object,
};

export default WidgetCard;
