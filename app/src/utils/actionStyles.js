import { colors } from 'src/utils/colors';

export const action = {
  primary: {
    border: `0.5px solid ${colors.border.secondary}`,
    borderRadius: '6px',
    height: '28px',
    width: '28px',
    backgroundColor: colors.background.white,
    img: {
      height: '18px',
      width: '18px',
      filter: 'brightness(0) saturate(100%) invert(76%) sepia(4%) saturate(11%) hue-rotate(318deg) brightness(84%) contrast(84%)',
    },
  },
  blueOutline: {
    border: `0.5px solid ${colors.border.secondary}`,
    borderRadius: '6px',
    height: '28px',
    width: '28px',
    backgroundColor: colors.background.white,
    img: {
      height: '18px',
      width: '18px',
      filter: 'brightness(0) saturate(100%) invert(76%) sepia(4%) saturate(11%) hue-rotate(318deg) brightness(84%) contrast(84%)',
    },
    '&:hover': {
      border: `0.75px solid ${colors.border.primaryLight}`,
      boxShadow: '0px 4px 6px -1px #3B82F61A',
      backgroundColor: colors.background.white,
      color: colors.text.primaryDark,
      '& img,svg,path': {
        filter: 'brightness(0) saturate(100%) invert(47%) sepia(47%) saturate(5039%) hue-rotate(203deg) brightness(100%) contrast(94%)',
      },
    },
  },
  investigateOutline: {
    border: `0.5px solid ${colors.border.secondary}`,
    borderRadius: '6px',
    height: '28px',
    width: '28px',
    backgroundColor: colors.background.white,
    boxShadow: '0px 1px 2px 0px #1018280D',
    img: {
      height: '18px',
      width: '18px',
      filter: 'brightness(0) saturate(100%) invert(19%) sepia(35%) saturate(413%) hue-rotate(177deg) brightness(102%) contrast(86%)',
    },
    '&:hover': {
      boxShadow: '0px 4px 6px -1px #3B82F61A',
      backgroundColor: colors.background.white,
      border: `0.5px solid ${colors.border.logo}`,
    },
  },
  delete: {
    border: `0.5px solid ${colors.border.secondary}`,
    borderRadius: '6px',
    height: '28px',
    width: '28px',
    backgroundColor: colors.background.white,
    img: {
      height: '18px',
      width: '18px',
      filter: 'brightness(0) saturate(100%) invert(76%) sepia(4%) saturate(11%) hue-rotate(318deg) brightness(84%) contrast(84%)',
    },
    '&:hover': {
      border: `0.5px solid ${colors.border.error}`,
      backgroundColor: '',
      '& img,svg,path': {
        filter: 'brightness(0) saturate(100%) invert(33%) sepia(94%) saturate(6534%) hue-rotate(353deg) brightness(101%) contrast(89%);  ',
      },
    },
  },

  nubi: {
    border: `0.5px solid ${colors.border.secondary}`,
    borderRadius: '6px',
    height: '28px',
    width: '28px',
    padding: '4px',
    backgroundColor: colors.background.white,
    img: {
      height: '18px',
      width: '18px',
    },
    '&:hover': {
      backgroundColor: colors.background.nubiHover,
    },
  },

  secondary: {
    height: '28px',
    width: '28px',
    backgroundColor: colors.background.transparent,
    img: {
      height: '20px',
      width: '20px',
      filter: 'brightness(0) saturate(100%) invert(76%) sepia(4%) saturate(11%) hue-rotate(318deg) brightness(84%) contrast(84%)',
    },
  },
};
