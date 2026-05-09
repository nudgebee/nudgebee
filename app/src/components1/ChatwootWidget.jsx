import { useEffect } from 'react';
import { useSession } from 'next-auth/react';

const ChatwootWidget = () => {
  const { data } = useSession({ required: true });

  // Initialize Chatwoot only once (bubble always hidden, opened via "Chat with us" menu)
  useEffect(() => {
    if (typeof window === 'undefined' || !data) {
      return;
    }

    if (!window.$chatwoot) {
      window.chatwootSettings = {
        hideMessageBubble: true,
        position: 'right',
        locale: 'en',
        type: 'standard',
      };

      (function (d, t) {
        const BASE_URL = 'https://app.chatwoot.com';
        const g = d.createElement(t),
          s = d.getElementsByTagName(t)[0];
        g.src = `${BASE_URL}/packs/js/sdk.js`;
        g.defer = true;
        g.async = true;
        s.parentNode.insertBefore(g, s);
        g.onload = function () {
          window.chatwootSDK.run({
            websiteToken: 'n5HTD9j1CbmyTdmCYpu1ZAxR',
            baseUrl: BASE_URL,
          });
        };
      })(document, 'script');

      window.addEventListener('chatwoot:ready', () => {
        window.$chatwoot?.setUser(data.user.email, {
          email: data.user.email,
          name: data.user.name,
        });
      });
    }

    // Keep bubble hidden after chat is closed — toggle('open') internally enables it,
    // so we re-hide it whenever the chat panel collapses.
    const handleClose = () => {
      window.$chatwoot?.toggleBubbleVisibility?.('hide');
      // Re-hide after Chatwoot's own state update completes
      setTimeout(() => {
        window.$chatwoot?.toggleBubbleVisibility?.('hide');
        document.querySelectorAll('.woot-widget-bubble, .woot--bubble-holder').forEach((el) => {
          el.style.setProperty('display', 'none', 'important');
        });
      }, 300);
    };

    window.addEventListener('chatwoot:close', handleClose);

    return () => {
      window.removeEventListener('chatwoot:close', handleClose);
    };
  }, [data]);

  return null;
};

export default ChatwootWidget;
