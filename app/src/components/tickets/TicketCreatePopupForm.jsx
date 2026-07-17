import Stack from '@mui/material/Stack';
import React, { useState } from 'react';
import { Modal } from '@ui/Modal';
import { Button } from '@ui/Button';
import TicketFormSection from './TicketFormSection';
import apiTickets from '@api1/tickets';
import { toast as snackbar } from '@ui/Toast';
import { ds } from '@utils/colors';

// Severity is sourced from the meta field tagged group === 'severity' and
// removed from additional_fields so a duplicate can't shadow the typed slot
// upstream. Returns { missingRequired } when required dynamic fields are empty.
export const buildCreateTicketPayload = (ticketState, { reference = {}, ticketData = {} } = {}) => {
  const { selectedConfig, selectedProject, selectedIssueType, ticketDetails, formData, selectedIssueTypeTicketMetadata } = ticketState;
  const fields = selectedIssueTypeTicketMetadata?.[0]?.fields ?? {};

  const isEmpty = (v) => v === null || v === undefined || v === '' || (Array.isArray(v) && v.length === 0);
  const missingRequired = Object.keys(fields).filter(
    (key) => key !== 'summary' && key !== 'description' && fields[key]?.required && isEmpty(formData?.[key])
  );
  if (missingRequired.length > 0) {
    return { missingRequired: missingRequired.map((key) => fields[key].name) };
  }

  const additionalFields = JSON.parse(JSON.stringify(formData ?? {}));
  delete additionalFields.assignee;

  const severityKey = Object.keys(fields).find((key) => fields[key]?.group === 'severity') ?? 'priority';
  const severity = formData?.[severityKey];
  delete additionalFields[severityKey];

  for (const [key, field] of Object.entries(fields)) {
    if (field?.type != 'datepicker' && field?.type != 'datetime') {
      continue;
    }
    // Empty here means an optional field left blank (required dates are seeded
    // on mount and enforced above) — omit it rather than defaulting to now.
    if (isEmpty(additionalFields[key])) {
      delete additionalFields[key];
    } else if (field.type == 'datepicker') {
      additionalFields[key] = new Date(additionalFields[key]).toISOString().split('T')[0];
    } else {
      additionalFields[key] = new Date(additionalFields[key]).toISOString();
    }
  }

  return {
    payload: {
      reference_id: reference?.id,
      ticket_type: selectedIssueType,
      integration_id: selectedConfig?.id,
      assignee: formData?.assignee,
      project_key: selectedProject?.key,
      title: ticketDetails?.subject,
      description: ticketDetails?.description,
      source: reference?.type,
      severity,
      account_id: ticketData?.accountId,
      additional_fields: additionalFields,
    },
  };
};

// When ticket creation fails upstream, the gateway returns tickets_create: null
// plus a GraphQL error whose message is a JSON-encoded copy of the handler
// payload; the human-readable reason lives at data.insert_tickets_one.error
// (e.g. "403 Repository was archived so is read-only" from GitHub).
export const extractCreateErrorMessage = (res) => {
  const gqlMessage = res?.data?.data?.errors?.[0]?.message;
  if (!gqlMessage) {
    return null;
  }
  try {
    return JSON.parse(gqlMessage)?.data?.insert_tickets_one?.error || null;
  } catch {
    return gqlMessage;
  }
};

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

    const { missingRequired, payload } = buildCreateTicketPayload(ticketState, { reference, ticketData });
    if (missingRequired) {
      snackbar.error(missingRequired.join(', ') + ' cannot be empty');
      return;
    }

    setError(false);
    setIsLoading(true);
    apiTickets
      .createTicket(payload)
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
          onFailure(responseData?.message || extractCreateErrorMessage(res) || 'Failed to create ticket');
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
          gap={ds.space[3]}
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
