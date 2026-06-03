import { IconButton } from '@mui/material';
import { EditOutlined } from '@mui/icons-material';
import { colors } from 'src/utils/colors';

interface EditButtonProps {
  onClick: () => void;
}

const EditButton: React.FC<EditButtonProps> = ({ onClick }) => {
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
      <EditOutlined />
    </IconButton>
  );
};

export default EditButton;
