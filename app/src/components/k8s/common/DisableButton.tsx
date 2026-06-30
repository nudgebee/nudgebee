import { IconButton } from '@mui/material';
import { DisabledByDefaultOutlined } from '@mui/icons-material';
import { colors } from 'src/utils/colors';

interface DisableButtonProps {
  onClick: () => void;
}

const DisableButton: React.FC<DisableButtonProps> = ({ onClick }) => {
  return (
    <IconButton
      style={{
        border: `0.5px solid ${colors.button.secondaryBorder}`,
        width: '32px',
        height: '32px',
        borderRadius: '4px',
        color: '#FCA5A5',
      }}
      onClick={onClick}
    >
      <DisabledByDefaultOutlined />
    </IconButton>
  );
};

export default DisableButton;
