import TicketsIcon from '@assets/sidebar-icon/tickets-icon.svg';
import { IconButton } from '@mui/material';
import SafeIcon from './SafeIcon';
import CustomTooltip from './CustomTooltip';

const CreateTicketButton = ({ onClick, sx }) => {
  return (
    <CustomTooltip title='Create Ticket' aria-label='Create Ticket'>
      <IconButton
        sx={{
          ...sx,
        }}
        onClick={onClick}
        aria-label='Create Ticket'
        id='create-ticket'
      >
        <SafeIcon priority={true} src={TicketsIcon} alt='Create Ticket' />
      </IconButton>
    </CustomTooltip>
  );
};

export default CreateTicketButton;
