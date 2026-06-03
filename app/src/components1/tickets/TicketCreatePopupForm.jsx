import Stack from '@mui/material/Stack';
import React, { useState } from 'react';
import { Modal } from '@components1/ds/Modal';
import { Button } from '@components1/ds/Button';
import TicketFormSection from './TicketFormSection';
import apiTickets from '@api1/tickets';
import { toast as snackbar } from '@components1/ds/Toast';

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
        const responseData = res?.data?.data?.data?.tickets_create?.data?.insert_tickets_one;
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
    <Modal
      open={open}
      handleClose={handleClose}
      title='Create Ticket'
      width='md'
      loader={isLoading}
      actionButtons={
        <Stack
          direction='row'
          sx={{
            float: 'right',
            button: {
              minWidth: '140px',
            },
          }}
          gap='12px'
        >
          <Button tone='secondary' size='md' onClick={handleCancel} disabled={isLoading}>
            Cancel
          </Button>
          <Button type='submit' size='md' onClick={createTicket} disabled={isLoading || ticketData?.accountId == 'demo'}>
            {buttonTitle}
          </Button>
        </Stack>
      }
    >
      <TicketFormSection
        ticketUrl={ticketUrl}
        ticketData={ticketData}
        error={error}
        onStateChange={(newState) => setTicketState(newState)}
        forceValidate={forceValidate}
      />
    </Modal>
  );
};

export default AddModalForm;
