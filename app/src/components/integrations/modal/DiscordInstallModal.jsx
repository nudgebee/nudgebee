import React, { useState, useEffect } from 'react';
import { Modal } from '@ui/Modal';
import { Input } from '@ui/Input';
import { Button as DsButton } from '@ui/Button';
import { Box, Typography } from '@mui/material';
import apiAccount from '@api1/account';
import { toast as snackbar } from '@ui/Toast';
import { ds } from 'src/utils/colors';
import { getIntegrationDescription } from '@lib/integrationDescriptions';

/**
 * Discord's install step: paste a bot token, and — if the bot isn't in any
 * server yet — walk through inviting it. Extracted from
 * `MessagingIntegrationTile.jsx`'s own inline `<Modal>` (same markup, same
 * `apiAccount.installDiscord` call) so Setup Center can open the real install
 * flow instead of the generic schema form, which has no bot-token field at
 * all (#41112). The real Integrations page still carries its own copy for
 * now — that component has no test coverage, so folding it onto this shared
 * one is a follow-up, not bundled into this fix.
 */
const DiscordInstallModal = ({ openModal, handleClose }) => {
  const [botToken, setBotToken] = useState('');
  const [isInstalling, setIsInstalling] = useState(false);
  const [inviteUrl, setInviteUrl] = useState(null);
  // Whether a token was ever successfully accepted this time the modal was
  // open — tracked separately from `inviteUrl` because the invite step is
  // optional; closing before the bot joins a server (or via the invite
  // step's own Done) must still report "saved" upstream.
  const [installed, setInstalled] = useState(false);

  useEffect(() => {
    if (openModal) {
      setBotToken('');
      setInviteUrl(null);
      setInstalled(false);
    }
  }, [openModal]);

  const closeModal = () => handleClose(installed);

  const handleInstall = async () => {
    if (!botToken) return;
    setIsInstalling(true);
    const result = await apiAccount.installDiscord(botToken);
    setIsInstalling(false);
    if (result?.success) {
      snackbar.success('Discord integration installed successfully!');
      setBotToken('');
      setInstalled(true);
      if (result?.guild_count === 0 && result?.invite_url) {
        // Token is valid but the bot isn't in any server — keep the modal open
        // and walk the user through the invite before they hunt for channels.
        setInviteUrl(result.invite_url);
      } else {
        handleClose(true);
      }
    } else {
      snackbar.error(result?.error || 'Failed to install Discord integration');
    }
  };

  return (
    <Modal
      width='sm'
      open={openModal}
      handleClose={closeModal}
      title={inviteUrl ? 'Invite the Bot to Your Server' : 'Install Discord Integration'}
      subtitle={getIntegrationDescription('discord')}
      loader={isInstalling}
      actionButtons={
        <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: ds.space[3], p: `${ds.space[3]} ${ds.space[5]}` }}>
          {inviteUrl ? (
            <DsButton id='done-install-modal-btn' size='md' tone='primary' onClick={closeModal}>
              Done
            </DsButton>
          ) : (
            <>
              <DsButton id='cancel-install-modal-btn' size='md' tone='secondary' onClick={closeModal} disabled={isInstalling}>
                Cancel
              </DsButton>
              <DsButton id='save-install-modal-btn' size='md' tone='primary' disabled={isInstalling || !botToken} onClick={handleInstall}>
                Install
              </DsButton>
            </>
          )}
        </Box>
      }
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: '20px', padding: '20px 24px' }}>
        {inviteUrl ? (
          <>
            <Typography sx={{ fontSize: ds.text.body, color: ds.brand[500], lineHeight: 1.5 }}>
              The bot token is saved, but the bot is not a member of any Discord server yet — no channels will be available until it is invited. Open
              the invite page, pick your server, and authorize the bot (it only requests View Channels and Send Messages permissions).
            </Typography>
            <DsButton id='open-discord-invite-btn' size='md' tone='primary' onClick={() => window.open(inviteUrl, '_blank', 'noopener')}>
              Open Discord Invite Page
            </DsButton>
          </>
        ) : (
          <Input
            id='bot-token'
            label='Discord Bot Token'
            required
            type='password'
            value={botToken}
            onChange={(val) => setBotToken(val)}
            placeholder='Enter your Discord bot token'
          />
        )}
      </Box>
    </Modal>
  );
};

export default DiscordInstallModal;
