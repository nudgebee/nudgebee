/**
 * Opens a provider's OAuth install flow as a popup, and calls `onClosed` once
 * the user closes it — win or walk away, there's no success/failure signal
 * from the popup itself, only its closing. Extracted from
 * `MessagingIntegrationTile.jsx`'s own `handleInstall` (same popup
 * dimensions, same close-poll), for Slack and Microsoft Teams specifically:
 * both are a single "open this URL" action with no form of their own, unlike
 * Discord (a bot-token modal) or Google Chat (no in-app trigger at all — see
 * `GoogleChatSpacesPanel.jsx`'s own doc comment). The real Integrations page
 * still carries its own copy of this logic for now — that component has no
 * test coverage, so folding it onto this shared one is a follow-up, not
 * bundled into this fix (#41112).
 */
export function openChatOAuthInstall(provider: string, installUrl: string, onClosed: () => void): void {
  const width = 600;
  const height = 700;
  const left = window.screenX + (window.outerWidth - width) / 2;
  const top = window.screenY + (window.outerHeight - height) / 2;
  const win = window.open(installUrl, `${provider}_install`, `popup,width=${width},height=${height},left=${left},top=${top}`);
  if (!win) {
    // Popup blocked — there's no window to poll, so report "closed" right
    // away instead of leaving the interval running forever.
    onClosed();
    return;
  }

  const intervalId = setInterval(() => {
    if (win.closed) {
      clearInterval(intervalId);
      onClosed();
    }
  }, 500);
}
