import { Box } from '@mui/material';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';

/**
 * WidgetCard - A reusable white card/widget component with consistent styling
 * Used across the application for displaying content in elevated white containers
 */
const WidgetCard = ({ children, sx = {}, ...props }) => {
  return (
    <Box
      sx={{
        border: `1px solid ${colors.border.secondaryLight}`,
        backgroundColor: colors.background.white,
        boxShadow: colors.shadow.card,
        padding: '20px 24px',
        borderRadius: '12px',
        mt: '24px',
        '@media(max-width: 1170px)': {
          padding: '16px !important',
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
