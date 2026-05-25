import { IconButton } from '@mui/material';
import { colors } from 'src/utils/colors';
import { DeleteIconRed } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';

interface DeleteButtonProps {
  onClick: () => void;
}

const DeleteButton: React.FC<DeleteButtonProps> = ({ onClick }) => {
  return (
    <IconButton
      style={{
        border: `0.5px solid ${colors.button.secondaryBorder}`,
        width: '32px',
        height: '32px',
        borderRadius: '4px',
      }}
      onClick={onClick}
    >
      <SafeIcon src={DeleteIconRed} alt='delete' width={20} height={20} />
    </IconButton>
  );
};

export default DeleteButton;
