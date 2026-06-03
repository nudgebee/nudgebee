import { modalSuccess, modalPasswordChange } from '@assets';
import Dialog from '@mui/material/Dialog';
import { Box, Typography, DialogContent, IconButton, DialogActions } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import { colors } from 'src/utils/colors';
import PropTypes from 'prop-types';
import LinearLoader from '@components1/k8s/common/LinearLoader';
import CustomButton from '@common/NewCustomButton';

export const Modal = ({
  open,
  width = 'sm',
  children,
  handleClose,
  onClose,
  title,
  subtitle,
  message = '',
  type = 1,
  icon = modalSuccess.default.src,
  onSuccess = false,
  rightComponentOnTitle,
  contentStyles,
  loader = false,
  actionButtons,
  sx = {},
  maxHeight,
  hideTitleBackground = false,
}) => {
  if (type == 'PASSWORD_CHANGE') {
    icon = modalPasswordChange.default.src;
  }
  return (
    <Dialog
      open={open}
      onClose={handleClose ?? onClose}
      aria-labelledby='alert-dialog-title'
      aria-describedby='alert-dialog-description'
      fullWidth={true}
      maxWidth={width}
      sx={{ ...sx, filter: loader ? 'blur(1px)' : 'none' }}
      PaperProps={{
        sx: {
          borderRadius: 'var(--ds-radius-xl)',
          ...(maxHeight && { maxHeight: maxHeight, height: maxHeight }),
        },
      }}
    >
      {loader && (
        <Box
          sx={{
            position: 'absolute',
            top: 0,
            left: 0,
            width: '100%',
            zIndex: 1,
          }}
        >
          <LinearLoader />
        </Box>
      )}
      <Box
        sx={{
          display: 'flex',
          justifyContent: 'space-between',
          padding: 'var(--ds-space-4) var(--ds-space-6)',
          ...(!hideTitleBackground && {
            borderBottom: `1px solid ${colors.border.primaryLight}`,
            background: 'var(--ds-blue-100)',
            boxShadow: '0px 2px 4px -2px rgba(0, 0, 0, 0.10)',
          }),
          alignItems: 'center',
        }}
      >
        <Box sx={{ display: 'flex', flexDirection: 'column' }}>
          <Typography
            sx={{
              fontSize: 'var(--ds-text-title)',
              fontWeight: 'var(--ds-font-weight-semibold)',
              color: colors.text.secondary,
              fontFamily: 'Poppins',
            }}
            fontWeight={600}
          >
            {title}
          </Typography>
          {subtitle && (
            <Typography
              sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)', color: 'var(--ds-gray-600)', mt: '0px' }}
            >
              {subtitle}
            </Typography>
          )}
        </Box>
        <Box display='flex' alignItems='center' justifyContent='flex-end'>
          {rightComponentOnTitle ? rightComponentOnTitle : undefined}
          <IconButton id='close-modal-btn' sx={{ padding: 0 }} onClick={handleClose ?? onClose}>
            <CloseIcon
              sx={{
                fontSize: 'var(--ds-text-heading)',
              }}
            />
          </IconButton>
        </Box>
      </Box>
      <DialogContent
        sx={{
          padding: '0px var(--ds-space-5) 0 var(--ds-space-5)',
          ...contentStyles,
          ...(maxHeight && { maxHeight, height: '100%' }),
        }}
      >
        {onSuccess && (
          <Box display='flex' flexDirection='column' justifyContent='space-between' alignItems='center' my='14px' mx='60px'>
            <Box
              component='img'
              sx={{
                height: '84px',
                width: '84px',
              }}
              alt='check'
              src={icon}
              mx='auto'
              mb='24px'
            />
            <Box
              align='center'
              mt='14px'
              color={colors.text.mid}
              sx={{
                fontSize: 'var(--ds-text-body-lg)',
                fontWeight: 'var(--ds-font-weight-regular)',
              }}
            >
              {message}
            </Box>
            <Box
              align='center'
              mt='14px'
              color={colors.text.mid}
              sx={{
                mt: 3,
                mb: 2,
                button: {
                  minWidth: '140px',
                },
              }}
            >
              <CustomButton size='Medium' variant='secondary' text='Close' onClick={handleClose ?? onClose} />
            </Box>
          </Box>
        )}
        <>{children}</>
      </DialogContent>
      {actionButtons && <DialogActions sx={{ display: 'inline', borderTop: '0.5px solid var(--ds-gray-200)' }}>{actionButtons}</DialogActions>}
    </Dialog>
  );
};

Modal.propTypes = {
  open: PropTypes.any,
  width: PropTypes.string,
  children: PropTypes.any,
  handleClose: PropTypes.func,
  onClose: PropTypes.func,
  title: PropTypes.any,
  subtitle: PropTypes.string,
  message: PropTypes.string,
  type: PropTypes.any,
  icon: PropTypes.any,
  onSuccess: PropTypes.bool,
  rightComponentOnTitle: PropTypes.any,
  contentStyles: PropTypes.object,
  loader: PropTypes.bool,
  actionButtons: PropTypes.any,
  sx: PropTypes.object,
  maxHeight: PropTypes.string,
  hideTitleBackground: PropTypes.bool,
};
