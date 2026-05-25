/**
 * CustomTicketLink — domain composition: "Ticket - {ID}" inline pattern with
 * optional auto-ellipsis and a fallback to plain Text when no URL is present.
 *
 * Built on `ds/Link`. Not a Link primitive duplicate — it's the prefix-label +
 * conditional-link + truncation pattern used in ticket lists and headers.
 *
 * For NEW code linking out to a ticket, prefer composing inline:
 *   `<Text value='Ticket -' secondaryText /> <Link href={url} target='_blank' secondaryText>{id}</Link>`
 */
import { Box } from '@mui/material';
import React from 'react';
import Text from './format/Text';
import { Link } from '@components1/ds/Link';
import PropTypes from 'prop-types';

const CustomTicketLink = ({ ticketURL, ticketID, showAutoEllipsis = false, maxWidth = '60px' }) => {
  return (
    <Box display={'flex'} alignItems={'center'}>
      <Text value={'Ticket -'} secondaryText />
      {ticketURL ? (
        <Link
          href={ticketURL}
          target='_blank'
          secondaryText
          style={showAutoEllipsis ? { maxWidth, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' } : {}}
        >
          {ticketID}
        </Link>
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
