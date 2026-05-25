import { useEffect, useRef, useState, useCallback } from 'react';
import Router from 'next/router';
import { snackbar } from '@components1/common/snackbarService';
import { getNubiIconUrl, getBrandTitle } from '@hooks/useTenantBranding';

const NOTIFY_PREF_KEY = 'nb_notify_on_complete';
const BANNER_SEEN_KEY = 'nb_notify_banner_seen';
const TERMINAL_STATUSES = ['COMPLETED', 'FAILED', 'KILLED', 'TERMINATED'];
const NOTIFY_STATUSES = [...TERMINAL_STATUSES, 'WAITING'];

/**
 * Hook for browser notifications on long-running operations.
 *
 * @param {Object} options
 * @param {boolean} options.isProcessing - Whether the operation is currently in progress
 * @param {string}  options.status - Current status of the operation (e.g. 'IN_PROGRESS', 'COMPLETED', 'FAILED')
 * @param {boolean} options.hasResults - Whether there are results to show (prevents firing on empty state)
 * @param {string}  [options.title] - Notification title (defaults to the tenant brand title from /api/app/config)
 * @param {string}  [options.navigateTo] - URL to navigate to when notification is clicked
 *
 * @returns {{ showBanner: boolean, handleEnable: () => void, handleDismiss: () => void }}
 */
export const useBrowserNotification = ({ isProcessing, status, hasResults, title, navigateTo }) => {
  const resolvedTitle = title || getBrandTitle();
  const [bannerDismissed, setBannerDismissed] = useState(true);
  const enabledRef = useRef(false);
  const lastNotifiedStatusRef = useRef(null);

  // Show banner or auto-enable when processing starts
  useEffect(() => {
    if (isProcessing) {
      if (localStorage.getItem(NOTIFY_PREF_KEY) === 'true') {
        enabledRef.current = true;
      } else if (localStorage.getItem(BANNER_SEEN_KEY) !== 'true') {
        setBannerDismissed(false);
      }
    }
  }, [isProcessing]);

  // Re-enable notifications when a new question starts processing
  useEffect(() => {
    if (status === 'IN_PROGRESS' && localStorage.getItem(NOTIFY_PREF_KEY) === 'true') {
      enabledRef.current = true;
      lastNotifiedStatusRef.current = null;
    }
  }, [status]);

  // Fire notification when operation reaches terminal state or needs user input
  useEffect(() => {
    if (!enabledRef.current) return;
    if (lastNotifiedStatusRef.current === status) return;
    const shouldNotify = NOTIFY_STATUSES.includes(status);
    if (shouldNotify && hasResults) {
      lastNotifiedStatusRef.current = status;
      if (TERMINAL_STATUSES.includes(status)) {
        enabledRef.current = false;
      }
      if (!('Notification' in window)) return;
      if (Notification.permission === 'granted' && document.hidden) {
        let body;
        if (status === 'COMPLETED') {
          body = 'Your investigation is ready!';
        } else if (status === 'WAITING') {
          body = 'A follow-up question needs your response';
        } else {
          body = `Investigation ${status.toLowerCase()}.`;
        }
        const notification = new Notification(resolvedTitle, { body, icon: getNubiIconUrl() });
        notification.onclick = () => {
          window.focus();
          if (navigateTo && Router.asPath !== navigateTo) {
            Router.push(navigateTo);
          }
          notification.close();
        };
      }
    }
  }, [status, hasResults, resolvedTitle, navigateTo]);

  const handleEnable = useCallback(() => {
    if (!('Notification' in window)) {
      snackbar.info('Browser notifications are not supported');
      return;
    }
    localStorage.setItem(BANNER_SEEN_KEY, 'true');
    if (Notification.permission === 'granted') {
      localStorage.setItem(NOTIFY_PREF_KEY, 'true');
      enabledRef.current = true;
      setBannerDismissed(true);
      snackbar.success("We'll notify you when the response is ready");
    } else if (Notification.permission === 'denied') {
      snackbar.error('Notifications are blocked. Please enable them in browser settings.');
    } else {
      Notification.requestPermission().then((permission) => {
        if (permission === 'granted') {
          localStorage.setItem(NOTIFY_PREF_KEY, 'true');
          enabledRef.current = true;
          setBannerDismissed(true);
          snackbar.success("We'll notify you when the response is ready");
        } else {
          snackbar.error('Notification permission denied');
        }
      });
    }
  }, []);

  const handleDismiss = useCallback(() => {
    localStorage.setItem(BANNER_SEEN_KEY, 'true');
    setBannerDismissed(true);
  }, []);

  return {
    showBanner: !bannerDismissed,
    handleEnable,
    handleDismiss,
  };
};
