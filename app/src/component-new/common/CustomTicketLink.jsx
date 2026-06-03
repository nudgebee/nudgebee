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
import { Box, Typography } from '@mui/material';
import Text from '@common-new/format/Text';
import { Link } from '@components1/ds/Link';
import { ds } from '@utils/colors';
import PropTypes from 'prop-types';

const CustomTicketLink = ({ ticketURL, ticketID, showAutoEllipsis = true, maxWidth = ds.space.mul(0, 60) }) => {
  return (
    <Box display='flex' alignItems='baseline' gap={ds.space[1]}>
      <Typography
        component='span'
        sx={{
          fontSize: 'var(--ds-text-small)',
          fontWeight: 'var(--ds-font-weight-regular)',
          color: 'var(--ds-gray-500)',
          lineHeight: 1,
          whiteSpace: 'nowrap',
        }}
      >
        Ticket -
      </Typography>
      {ticketURL ? (
        <Link href={ticketURL} secondaryText openInNew maxWidth={showAutoEllipsis ? maxWidth : undefined}>
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
