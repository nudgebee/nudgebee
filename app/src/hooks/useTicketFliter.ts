import { useState } from 'react';
import { snakeToTitleCase, type SnackBarProps } from 'src/utils/common';
import { TicketsIcon } from '@assets';
import { v4 as uuidv4 } from 'uuid';
import { md5 } from '@lib/encode';

const useTicketFliter = () => {
  const [ticketData, setTicketData] = useState<any>({});
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [snackbarData, setSnackbarData] = useState<SnackBarProps>({ message: '', severity: 'success' });
  const [isSnackBarOpen, setIsSnackBarOpen] = useState<boolean>(false);

  const getMenuItem = () => {
    const MENU_ITEMS = [
      {
        icon: TicketsIcon,
        label: 'Create Ticket',
        id: 0,
      },
    ];
    return MENU_ITEMS;
  };

  const onMenuClick = (menuItem: any, data: any) => {
    if (menuItem.id === 0) {
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    }
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const closeSnackBarOpen = () => {
    setIsSnackBarOpen(false);
  };

  const getTicketDescription = (data: any): string => {
    if (!data) {
      return '';
    }

    let description = '';

    const flatten = (obj: any): any => {
      let result: any = {};
      for (const key in obj) {
        if (typeof obj[key] === 'object' && obj[key] !== null) {
          result = { ...result, ...flatten(obj[key]) };
        } else {
          result[key] = obj[key];
        }
      }
      return result;
    };

    const flatData = flatten(data);

    for (const key in flatData) {
      description += ` ${snakeToTitleCase(key)}: ${flatData[key]}\n`;
    }

    return description;
  };
  const handleTicketSuccess = () => {};

  const handleTicketFailure = (res: string) => {
    setSnackbarData({ message: `Failed! ${res}.`, severity: 'error' });
    setIsSnackBarOpen(true);
  };

  const getTicketReferenceId = (ticketData: any) => {
    const message = ticketData?.data;
    const stream = ticketData?.stream?.labels || {};
    if (message && stream) {
      try {
        if (message) {
          return md5([stream.app, stream.container, stream.namespace, message]);
        }
      } catch {
        return md5([stream.app, stream.container, stream.namespace, message]);
      }
    }
    return uuidv4();
  };

  return {
    ticketData,
    isTicketCreateFormOpen,
    snackbarData,
    isSnackBarOpen,
    getMenuItem,
    onMenuClick,
    closeTicketCreateForm,
    closeSnackBarOpen,
    getTicketDescription,
    getTicketReferenceId,
    handleTicketSuccess,
    handleTicketFailure,
  };
};

export default useTicketFliter;
