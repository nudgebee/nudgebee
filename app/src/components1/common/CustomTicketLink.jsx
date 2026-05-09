import { Box } from '@mui/material';
import React from 'react';
import Text from './format/Text';
import CustomLink from './CustomLink';
import PropTypes from 'prop-types';

const CustomTicketLink = ({ ticketURL, ticketID, showAutoEllipsis = false, maxWidth = '60px' }) => {
  return (
    <Box display={'flex'} alignItems={'center'}>
      <Text value={'Ticket -'} secondaryText />
      {ticketURL ? (
        <CustomLink
          href={ticketURL}
          target='_blank'
          secondaryText
          style={showAutoEllipsis ? { maxWidth, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' } : {}}
        >
          {ticketID}
        </CustomLink>
      ) : (
        <Text value={ticketID} showAutoEllipsis={showAutoEllipsis} sx={showAutoEllipsis ? { maxWidth } : {}} />
      )}
    </Box>
  );
};

CustomTicketLink.propTypes = {
  ticketURL: PropTypes.string.isRequired,
  ticketID: PropTypes.string.isRequired,
  showAutoEllipsis: PropTypes.bool,
  maxWidth: PropTypes.string,
};

export default CustomTicketLink;
