/**
 * The per-row quick actions every recommendation listing shares — Optimize and
 * Ask Nubi as icon buttons, ticket / dismiss / CLI behind the overflow menu.
 *
 * Lives in its own file so the Configuration tab's findings rows can carry the
 * same actions as the Cost tab's rows without importing OptimizeNewPage (which
 * imports the components that would import this — a cycle).
 */
import { memo } from 'react';
import { Box } from '@mui/material';
import ContentCopyOutlinedIcon from '@mui/icons-material/ContentCopyOutlined';
import ConfirmationNumberOutlinedIcon from '@mui/icons-material/ConfirmationNumberOutlined';
import DoNotDisturbOnOutlinedIcon from '@mui/icons-material/DoNotDisturbOnOutlined';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import { Button } from '@ui/Button';
import Tooltip from '@ui/Tooltip';
import { DropdownMenu } from '@ui/DropdownMenu';
import SafeIcon from '@shared/icons/SafeIcon';
import OptimizeIcon from 'src/assets/images/home/optimize-icon-button.svg';
import { getNubiIconUrl } from '@hooks/useTenantBranding';
import { hasPermission, hasWriteAccess } from '@lib/auth';
import { ds } from 'src/utils/colors';

/**
 * Keeps a hover hint underneath an open menu.
 *
 * MUI's z-index scale puts `tooltip` (1500) above `modal` (1300), so a tooltip
 * still fading out as the overflow menu opens paints over the menu items. These
 * buttons sit shoulder to shoulder, so moving from one to the next is the normal
 * way to reach the menu and the overlap is easy to hit.
 *
 * Passed as `slotProps`, not `PopperProps`: ds/Tooltip spreads its own rest AFTER
 * its PopperProps, so supplying that key would replace the flip and
 * preventOverflow modifiers it sets.
 */
const TOOLTIP_UNDER_MENU = { popper: { sx: { zIndex: 'modal' } } } as const;

interface RowActionsProps {
  rowId: string;
  rec: any;
  ticketId: string;
  assistantName: string | undefined;
  onAskNubi: (rec: any) => void;
  onResolve: (rec: any) => void;
  onCreateTicket: (rec: any) => void;
  onCopyCli: (rec: any) => void;
  onDismiss: (rec: any) => void;
}

const RowActions = memo(({ rowId, rec, ticketId, assistantName, onAskNubi, onResolve, onCreateTicket, onCopyCli, onDismiss }: RowActionsProps) => {
  const showResolve = rec.rule_name === 'pod_right_sizing' && hasWriteAccess(rec.account_id);
  const showCopyCli = rec.rule_name === 'pod_right_sizing';
  // Only offer what the backend legality matrix accepts: dismiss from Open,
  // reactivate from Dismissed. Other statuses (InProgress, Closed, Archive) get
  // no menu entry rather than a guaranteed error toast.
  const canDismiss = hasWriteAccess(rec.account_id) && (!rec.status || rec.status === 'Open' || rec.status === 'Dismissed');
  // Creating a ticket needs write access to the row's account OR the
  // tickets:Write custom-role grant (tickets_create → tickets:Write). Disabled
  // rather than dropped so an existing ticket id stays readable.
  const canCreateTicket = hasWriteAccess(rec.account_id) || hasPermission('tickets', 'Write');

  const menuItems: Array<{ label: string; icon: React.ReactNode; onSelect: () => void; disabled?: boolean; id?: string }> = [
    {
      id: `action-ticket-${rowId}`,
      label: ticketId ? `Ticket: ${ticketId}` : 'Create ticket',
      icon: <ConfirmationNumberOutlinedIcon sx={{ fontSize: 16 }} />,
      onSelect: () => onCreateTicket(rec),
      disabled: !!ticketId || !canCreateTicket,
    },
    ...(canDismiss
      ? [
          {
            id: `action-dismiss-${rowId}`,
            label: rec.status === 'Dismissed' ? 'Reactivate' : 'Dismiss / snooze',
            icon: <DoNotDisturbOnOutlinedIcon sx={{ fontSize: 16 }} />,
            onSelect: () => onDismiss(rec),
          },
        ]
      : []),
    ...(showCopyCli
      ? [
          {
            id: `action-copy-cli-${rowId}`,
            label: 'Copy CLI command',
            icon: <ContentCopyOutlinedIcon sx={{ fontSize: 16 }} />,
            onSelect: () => onCopyCli(rec),
          },
        ]
      : []),
  ];

  return (
    <Box onClick={(e) => e.stopPropagation()} sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1], justifyContent: 'flex-end' }}>
      {showResolve && (
        <Tooltip title='Optimize' placement='top' slotProps={TOOLTIP_UNDER_MENU}>
          <span>
            <Button
              tone='ghost'
              size='xs'
              composition='icon-only'
              icon={<SafeIcon src={OptimizeIcon} alt='' width={16} height={16} />}
              aria-label='Optimize'
              id={`action-resolve-${rowId}`}
              onClick={() => onResolve(rec)}
            />
          </span>
        </Tooltip>
      )}
      <Tooltip title={`Ask ${assistantName || 'Nubi'}`} placement='top' slotProps={TOOLTIP_UNDER_MENU}>
        <span>
          <Button
            tone='ghost'
            size='xs'
            composition='icon-only'
            icon={<SafeIcon src={getNubiIconUrl()} alt='' width={16} height={16} />}
            aria-label={`Ask ${assistantName || 'Nubi'}`}
            id={`action-ask-nubi-${rowId}`}
            onClick={() => onAskNubi(rec)}
          />
        </span>
      </Tooltip>
      <DropdownMenu
        align='end'
        size='sm'
        items={menuItems}
        trigger={
          <Button tone='ghost' size='xs' composition='icon-only' icon={<MoreVertIcon />} aria-label='More actions' id={`action-menu-${rowId}`} />
        }
      />
    </Box>
  );
});
RowActions.displayName = 'RowActions';

export default RowActions;
