import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';

const CustomDropdownIcon = ({ color, props }: { color: string; props: any }) => {
  return <KeyboardArrowDownIcon sx={{ height: '1.1em', width: '1em', color: color }} {...props} />;
};

export default CustomDropdownIcon;
