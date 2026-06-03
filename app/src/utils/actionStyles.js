export const action = {
  primary: {
    border: '0.5px solid var(--ds-gray-300)',
    borderRadius: 'var(--ds-radius-md)',
    height: '28px',
    width: '28px',
    backgroundColor: 'var(--ds-background-100)',
    img: {
      height: '18px',
      width: '18px',
      filter: 'brightness(0) saturate(100%) invert(76%) sepia(4%) saturate(11%) hue-rotate(318deg) brightness(84%) contrast(84%)',
    },
  },
  blueOutline: {
    border: '0.5px solid var(--ds-gray-300)',
    borderRadius: 'var(--ds-radius-md)',
    height: '28px',
    width: '28px',
    backgroundColor: 'var(--ds-background-100)',
    img: {
      height: '18px',
      width: '18px',
      filter: 'brightness(0) saturate(100%) invert(76%) sepia(4%) saturate(11%) hue-rotate(318deg) brightness(84%) contrast(84%)',
    },
    '&:hover': {
      border: '0.75px solid var(--ds-blue-300)',
      boxShadow: '0px 4px 6px -1px #3B82F61A',
      backgroundColor: 'var(--ds-background-100)',
      color: 'var(--ds-blue-500)',
      '& img,svg,path': {
        filter: 'brightness(0) saturate(100%) invert(47%) sepia(47%) saturate(5039%) hue-rotate(203deg) brightness(100%) contrast(94%)',
      },
    },
  },
  investigateOutline: {
    border: '0.5px solid var(--ds-gray-300)',
    borderRadius: 'var(--ds-radius-md)',
    height: '28px',
    width: '28px',
    backgroundColor: 'var(--ds-background-100)',
    boxShadow: '0px 1px 2px 0px #1018280D',
    img: {
      height: '18px',
      width: '18px',
      filter: 'brightness(0) saturate(100%) invert(19%) sepia(35%) saturate(413%) hue-rotate(177deg) brightness(102%) contrast(86%)',
    },
    '&:hover': {
      boxShadow: '0px 4px 6px -1px #3B82F61A',
      backgroundColor: 'var(--ds-background-100)',
      border: '0.5px solid var(--ds-yellow-500)',
    },
  },
  delete: {
    border: '0.5px solid var(--ds-gray-300)',
    borderRadius: 'var(--ds-radius-md)',
    height: '28px',
    width: '28px',
    backgroundColor: 'var(--ds-background-100)',
    img: {
      height: '18px',
      width: '18px',
      filter: 'brightness(0) saturate(100%) invert(76%) sepia(4%) saturate(11%) hue-rotate(318deg) brightness(84%) contrast(84%)',
    },
    '&:hover': {
      border: '0.5px solid var(--ds-red-500)',
      backgroundColor: '',
      '& img,svg,path': {
        filter: 'brightness(0) saturate(100%) invert(33%) sepia(94%) saturate(6534%) hue-rotate(353deg) brightness(101%) contrast(89%);  ',
      },
    },
  },

  nubi: {
    border: '0.5px solid var(--ds-gray-300)',
    borderRadius: 'var(--ds-radius-md)',
    height: '28px',
    width: '28px',
    padding: 'var(--ds-space-1)',
    backgroundColor: 'var(--ds-background-100)',
    img: {
      height: '18px',
      width: '18px',
    },
    '&:hover': {
      backgroundColor: 'var(--ds-yellow-100)',
    },
  },

  secondary: {
    height: '28px',
    width: '28px',
    backgroundColor: 'transparent',
    img: {
      height: '20px',
      width: '20px',
      filter: 'brightness(0) saturate(100%) invert(76%) sepia(4%) saturate(11%) hue-rotate(318deg) brightness(84%) contrast(84%)',
    },
  },
};
