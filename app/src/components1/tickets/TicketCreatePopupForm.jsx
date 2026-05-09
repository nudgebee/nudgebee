import { DialogContent, DialogContentText, Typography } from '@mui/material';
import Stack from '@mui/material/Stack';
import React, { useState } from 'react';
import Box from '@mui/material/Box';
import Dialog from '@mui/material/Dialog';
import LinearLoader from '@components1/k8s/common/LinearLoader';
import CustomButton from '@components1/common/NewCustomButton';
import TicketFormSection from './TicketFormSection';
import apiTickets from '@api1/tickets';
import { snackbar } from '@components1/common/snackbarService';
import { colors } from 'src/utils/colors';

const AddModalForm = ({
  ticketUrl = {},
  open,
  handleClose,
  onClose,
  onSuccess,
  onFailure,
  buttonTitle = 'Create Ticket',
  ticketData = {},
  reference = {},
}) => {
  const [isLoading, setIsLoading] = useState(false);
  const [ticketState, setTicketState] = useState({});
  const [error, setError] = useState(false);
  const [forceValidate, setForceValidate] = useState(false);

  const createTicket = async function () {
    setForceValidate(true);

    if (!ticketState.isValid) {
      snackbar.error('Please fill all required fields before creating a ticket.');
      return;
    }

    const { selectedConfig, selectedProject, selectedIssueType, ticketDetails, formData, selectedIssueTypeTicketMetadata } = ticketState;

    setError(false);
    setIsLoading(true);
    let cloneObj = JSON.parse(JSON.stringify(formData));
    delete cloneObj.assignee;
    for (const [_key, value] of Object.entries(selectedIssueTypeTicketMetadata?.[0]?.fields ?? [])) {
      if (value && value.type == 'datepicker') {
        cloneObj[_key] = cloneObj[_key] ? new Date(cloneObj[_key]).toISOString().split('T')[0] : new Date().toISOString().split('T')[0];
      } else if (value && value.type == 'datetime') {
        cloneObj[_key] = cloneObj[_key] ? new Date(cloneObj[_key]).toISOString() : new Date().toISOString();
      }
    }
    const required = Object.keys(selectedIssueTypeTicketMetadata?.[0]?.fields ?? {}).filter(
      (_key) =>
        _key !== 'summary' && _key !== 'description' && selectedIssueTypeTicketMetadata[0].fields[_key]?.required && !Object.hasOwn(cloneObj, _key)
    );
    const labels = required.map((_key) => selectedIssueTypeTicketMetadata?.[0].fields[_key].name);
    if (labels && labels.length > 0) {
      snackbar.error(labels.join(', ') + ' cannot be empty');
      return;
    }
    setIsLoading(true);
    apiTickets
      .createTicket({
        reference_id: reference?.id,
        ticket_type: selectedIssueType,
        integration_id: selectedConfig?.id,
        assignee: formData?.assignee,
        project_key: selectedProject?.key,
        title: ticketDetails?.subject,
        description: ticketDetails?.description,
        source: reference?.type,
        severity: formData?.priority,
        account_id: ticketData?.accountId,
        additional_fields: cloneObj,
      })
      .then((res) => {
        const responseData = res?.data?.data?.data?.tickets_insert_one?.data?.insert_tickets_one;
        if (responseData?.error) {
          onFailure(responseData?.error);
        } else if (responseData?.action == 'created') {
          handleCancel();
          if (onSuccess) {
            onSuccess(responseData?.message);
          }
          const ticketId = responseData?.ticket_id;
          const ticketUrl = responseData?.url;
          if (ticketId && ticketUrl) {
            snackbar.success(
              <span>
                Ticket <b>{ticketId}</b> created.{' '}
                <a href={ticketUrl} target='_blank' rel='noopener noreferrer' style={{ color: 'inherit', textDecoration: 'underline' }}>
                  View ticket
                </a>
              </span>
            );
          } else {
            snackbar.success(responseData?.message || 'Ticket created successfully');
          }
        } else if (responseData?.action == 'commented') {
          handleCancel();
          const ticketUrl = responseData?.url;
          if (ticketUrl) {
            snackbar.warning(
              <span>
                {responseData?.message || 'Existing ticket found, added comment.'}{' '}
                <a href={ticketUrl} target='_blank' rel='noopener noreferrer' style={{ color: 'inherit', textDecoration: 'underline' }}>
                  View ticket
                </a>
              </span>
            );
          } else {
            snackbar.warning(responseData?.message);
          }
        } else {
          onFailure(responseData?.message || 'Failed to create ticket');
        }
      })
      .catch(() => {
        onFailure('Failed to create ticket');
      })
      .finally(() => {
        setIsLoading(false);
      });
  };

  const handleCancel = () => {
    setForceValidate(false);
    setError(false);
    setIsLoading(false);
    onClose();
  };

  return (
    <Dialog
      open={open}
      onClose={handleClose}
      aria-labelledby='alert-dialog-title'
      aria-describedby='alert-dialog-description'
      maxWidth='md'
      fullWidth
    >
      {isLoading && (
        <Box sx={{ position: 'absolute', top: 0, left: 0, right: 0, zIndex: 9999 }}>
          <LinearLoader />
        </Box>
      )}
      <DialogContent
        sx={{
          flexGrow: 1,
          overflowY: 'auto',
          padding: '0px',
          paddingLeft: '16px',
          paddingRight: '16px',
          '&.MuiDialogContent-root': {
            padding: '0px',
            paddingLeft: '16px',
            paddingRight: '16px',
          },
        }}
      >
        <DialogContentText id='alert-dialog-description'>
          <Box display='flex' justifyContent='end' />
          <Box display='flex' flexDirection='column' justifyContent='space-between' alignItems='left' my='5px' mx='10px' py='1px'>
            <Box
              sx={{
                p: 2,
                borderBottom: '1px solid rgba(0, 0, 0, 0.12)',
                position: 'sticky',
                top: 0,
                backgroundColor: 'white',
                zIndex: 100,
              }}
            >
              <Typography component='h2' variant='h5' fontWeight={600} color={colors.text.signinDark}>
                {'Create Ticket'}
              </Typography>
            </Box>
            <TicketFormSection
              ticketUrl={ticketUrl}
              ticketData={ticketData}
              error={error}
              onStateChange={(newState) => setTicketState(newState)}
              forceValidate={forceValidate}
            />
            <Box
              sx={{
                margin: '16px 0px',
              }}
            />
            <Box
              sx={{
                borderTop: '1px solid rgba(0, 0, 0, 0.12)',
                position: 'sticky',
                bottom: 0,
                backgroundColor: 'white',
                zIndex: 1,
              }}
            >
              <Stack
                direction='row'
                sx={{
                  float: 'right',
                  button: {
                    minWidth: '140px',
                  },
                }}
                mb={2}
                mt='12px'
                gap='12px'
              >
                <CustomButton variant='secondary' size='Medium' text={'Cancel'} onClick={handleCancel} disabled={isLoading} />
                <CustomButton
                  type='submit'
                  size='Medium'
                  text={buttonTitle}
                  onClick={() => {
                    createTicket();
                  }}
                  disabled={isLoading || ticketData?.accountId == 'demo'}
                />
              </Stack>
            </Box>
          </Box>
        </DialogContentText>
      </DialogContent>
    </Dialog>
  );
};

export default AddModalForm;
