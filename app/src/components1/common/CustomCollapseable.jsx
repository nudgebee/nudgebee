import React, { useState, useEffect } from 'react';
import { Accordion, AccordionSummary, AccordionDetails, Typography, Box } from '@mui/material';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import PropTypes from 'prop-types';

const CustomCollapseable = ({ title, children, sx = {}, icon, defaultExpand = false }) => {
  const [expanded, setExpanded] = useState(defaultExpand);

  useEffect(() => {
    setExpanded(defaultExpand);
  }, [defaultExpand]);

  const handleChange = (event, isExpanded) => {
    setExpanded(isExpanded);
  };

  return (
    <Box sx={{ width: '100%', marginTop: 'var(--ds-space-4)' }}>
      <Accordion expanded={expanded} sx={{ ...sx, pb: expanded && '15px' }} onChange={handleChange}>
        <AccordionSummary
          expandIcon={icon || <ExpandMoreIcon />}
          aria-controls={`${title}-content`}
          id={`${title}-header`}
          sx={{
            '& .MuiAccordionSummary-expandIconWrapper': {
              transform: 'none !important',
              transition: 'none !important',
            },
            '& .expand-icon': {
              position: 'absolute',
              top: '14px',
              right: '-12px',
            },
            '&.MuiButtonBase-root': {
              minHeight: expanded ? '0px' : 'auto',
            },
          }}
        >
          {!expanded && <Typography>{title}</Typography>}
        </AccordionSummary>
        <AccordionDetails>{children}</AccordionDetails>
      </Accordion>
    </Box>
  );
};

CustomCollapseable.propTypes = {
  title: PropTypes.string.isRequired,
  children: PropTypes.node,
  sx: PropTypes.object,
  icon: PropTypes.any,
  defaultExpand: PropTypes.bool,
};

export default CustomCollapseable;
