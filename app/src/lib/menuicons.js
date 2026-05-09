import SidebarDrawerIcon from '@assets/sidebar-icon/drawer-arrows-icon.svg';
import HomeIcon from '@assets/sidebar-icon/home-icon.svg';
import AwardIcon from '@assets/sidebar-icon/award-icon.svg';
import BriefcaseIcon from '@assets/sidebar-icon/briefcase-icon.svg';
import AccountsIcon from '@assets/sidebar-icon/accounts-icon.svg';
import MyKubernetesIcon from '@assets/sidebar-icon/my-kubernetes-icon.svg';
import PageIcon from '@assets/sidebar-icon/page-icon.svg';
import SnowBeeIcon from '@assets/sidebar-icon/snow-bee.svg';
import RedBeeIcon from '@assets/sidebar-icon/red-bee.svg';
import DBBeeIcon from '@assets/sidebar-icon/db-bee.svg';
import SpotBeeIcon from '@assets/sidebar-icon/spot-bee.svg';
import MarketBeeIcon from '@assets/sidebar-icon/market-bee.svg';
import SnowflakeIcon from '@assets/sidebar-icon/snowflake-icon.svg';
import { Box } from '@mui/material';

const BarIcons = ({ icons }) => {
  let icon = null;
  if (icons == null) {
    icon = HomeIcon;
  } else if (icons.toUpperCase() === 'AwardIcon') {
    icon = AwardIcon;
  } else if (icons.toUpperCase() === 'BriefcaseIcon') {
    icon = BriefcaseIcon;
  } else if (icons.toUpperCase() === 'AccountsIcon') {
    icon = AccountsIcon;
  } else if (icons.toUpperCase() === 'MyKubernetesIcon') {
    icon = MyKubernetesIcon;
  } else if (icons.toUpperCase() === 'PageIcon') {
    icon = PageIcon;
  } else if (icons.toUpperCase() === 'SnowBeeIcon') {
    icon = SnowBeeIcon;
  } else if (icons.toUpperCase() === 'RedBeeIcon') {
    icon = RedBeeIcon;
  } else if (icons.toUpperCase() === 'DBBeeIcon') {
    icon = DBBeeIcon;
  } else if (icons.toUpperCase() === 'SpotBeeIcon') {
    icon = SpotBeeIcon;
  } else if (icons.toUpperCase() === 'MarketBeeIcon') {
    icon = MarketBeeIcon;
  } else if (icons.toUpperCase() === 'SnowflakeIcon') {
    icon = SnowflakeIcon;
  }

  return icon ? (
    <Box
      component='img'
      sx={{
        height: '28px',
        width: '28px',
      }}
      alt='aws'
      src={icon}
    />
  ) : (
    <Box
      component='img'
      sx={{
        height: '28px',
        width: '28px',
      }}
      alt='aws'
      src={SidebarDrawerIcon}
    />
  );
};
export default BarIcons;
