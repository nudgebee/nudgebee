import { Box, BoxProps } from '@mui/material';
import PropTypes from 'prop-types';
import { ds } from 'src/utils/colors';

/**
 * WidgetCard - A reusable white card/widget component with consistent styling
 * Used across the application for displaying content in elevated white containers
 */
const WidgetCard = ({ children, sx = {}, ...props }: BoxProps) => {
  return (
    <Box
      sx={{
        border: `1px solid ${ds.gray[200]}`,
        backgroundColor: ds.background[100],
        boxShadow: '0px 4px 20px -1px rgba(229, 229, 229, 0.4), 0px 2px 20px 0px rgb(233, 233, 233)',
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
