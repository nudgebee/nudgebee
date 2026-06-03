import React from 'react';
import { Accordion, AccordionSummary, AccordionDetails, Typography, Box } from '@mui/material';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import { styles } from '@components1/runbooks/styles';
import PropTypes from 'prop-types';

const CustomAccordion = ({
  title,
  description,
  icon,
  children,
  expanded,
  onChange,
  accordionStyle = {},
  summaryStyle = {},
  titleStyle = {},
  descriptionStyle = {},
  detailsStyle = {},
}) => {
  const formattedTitle = title.replace(/ /g, '-');
  const formattedId = `panel-header-${formattedTitle}`;

  return (
    <Accordion id={formattedId} sx={{ ...styles.accordion, ...accordionStyle }} expanded={expanded} onChange={onChange} elevation={0} disableGutters>
      <AccordionSummary expandIcon={children ? <ExpandMoreIcon /> : null} aria-controls={`panel-content-${formattedTitle}`} sx={{ ...summaryStyle }}>
        <Box display='flex' alignItems={!expanded ? 'center' : 'flex-start'} gap={'10px'}>
          {icon && <Box pt={'3px'}>{icon}</Box>}
          <Box>
            <Typography
              variant='subtitle1'
              sx={{
                color: 'var(--ds-brand-500)',
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-medium)',
                ...titleStyle,
              }}
            >
              {title}
            </Typography>
            {description && (
              <Typography
                variant='body2'
                sx={{
                  color: 'var(--ds-gray-400)',
                  fontSize: 'var(--ds-text-caption)',
                  fontWeight: 'var(--ds-font-weight-regular)',
                  ...descriptionStyle,
                }}
              >
                {description}
              </Typography>
            )}
          </Box>
        </Box>
      </AccordionSummary>
      <AccordionDetails sx={{ ...detailsStyle }}>{children}</AccordionDetails>
    </Accordion>
  );
};

export default CustomAccordion;

CustomAccordion.propTypes = {
  title: PropTypes.string,
  description: PropTypes.string,
  icon: PropTypes.any,
  children: PropTypes.any,
  expanded: PropTypes.bool,
  onChange: PropTypes.func,
  accordionStyle: PropTypes.object,
  summaryStyle: PropTypes.object,
  titleStyle: PropTypes.object,
  descriptionStyle: PropTypes.object,
  detailsStyle: PropTypes.object,
};
