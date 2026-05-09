/**
 * IconTextBadge - Generic reusable component for displaying an icon with text
 *
 * Can be used standalone with custom icons or with built-in presets (e.g., platforms)
 *
 * Usage Examples:
 *
 * 1. Custom icon:
 * <IconTextBadge icon={MyIcon} text="My Label" />
 *
 * 2. MUI icon:
 * <IconTextBadge muiIcon={<SettingsIcon />} text="Settings" />
 *
 * 3. Platform preset:
 * <IconTextBadge preset="slack" text="#alerts-prod" />
 *
 * 4. With custom tooltip:
 * <IconTextBadge icon={MyIcon} text="Label" tooltip="Custom tooltip text" />
 */

import React, { ReactNode } from 'react';
import { Box, Tooltip, Typography, SxProps, Theme } from '@mui/material';
import MailOutlineIcon from '@mui/icons-material/MailOutline';
import { SlackIcon, MSTeamsIcon, GChatIcon, jiraIcon, serviceNowIcon, PagerDutyIcon } from '@assets';
import { colors } from 'src/utils/colors';
import SafeIcon from './SafeIcon';

// Built-in presets for common use cases
const PRESETS: Record<string, { icon: any; label: string; muiIcon?: ReactNode }> = {
  slack: { icon: SlackIcon, label: 'Slack' },
  ms_teams: { icon: MSTeamsIcon, label: 'Teams' },
  google_chat: { icon: GChatIcon, label: 'Google Chat' },
  email: { icon: null, label: 'Email', muiIcon: <MailOutlineIcon /> },
  jira: { icon: jiraIcon, label: 'Jira' },
  servicenow: { icon: serviceNowIcon, label: 'ServiceNow' },
  pagerduty: { icon: PagerDutyIcon, label: 'PagerDuty' },
};

interface IconTextBadgeProps {
  // Text to display
  text: string;

  // Icon options (use one of these)
  icon?: any; // Static import or require() image
  muiIcon?: ReactNode; // MUI icon component
  preset?: keyof typeof PRESETS | string; // Built-in preset name

  // Styling
  size?: 'small' | 'medium' | 'large';
  maxWidth?: number;
  iconSize?: number;
  textColor?: string;
  sx?: SxProps<Theme>;

  // Behavior
  tooltip?: string | boolean; // Custom tooltip text, true for auto-generated, false to disable
  onClick?: () => void;
}

const IconTextBadge: React.FC<IconTextBadgeProps> = ({
  text,
  icon,
  muiIcon,
  preset,
  size = 'medium',
  maxWidth,
  iconSize: customIconSize,
  textColor,
  sx,
  tooltip = true,
  onClick,
}) => {
  // Resolve preset config if provided
  const presetConfig = preset ? PRESETS[preset] : null;

  // Determine icon to use (priority: explicit icon > explicit muiIcon > preset)
  const resolvedIcon = icon || presetConfig?.icon;
  const resolvedMuiIcon = muiIcon || presetConfig?.muiIcon;

  // Size configurations
  const sizeConfig = {
    small: { iconSize: 12, fontSize: '11px', gap: '4px', maxWidth: maxWidth || 100 },
    medium: { iconSize: 16, fontSize: '13px', gap: '6px', maxWidth: maxWidth || 140 },
    large: { iconSize: 20, fontSize: '14px', gap: '8px', maxWidth: maxWidth || 180 },
  };

  const config = sizeConfig[size];
  const iconSizeValue = customIconSize || config.iconSize;

  // Render the icon
  const renderIcon = () => {
    if (resolvedMuiIcon) {
      return React.cloneElement(resolvedMuiIcon as React.ReactElement, {
        sx: { fontSize: iconSizeValue, color: textColor || colors.text.tertiary, flexShrink: 0 },
      });
    }

    if (resolvedIcon) {
      const iconSrc = resolvedIcon.src || resolvedIcon.default || resolvedIcon;
      return (
        <SafeIcon
          src={iconSrc}
          alt={presetConfig?.label || 'icon'}
          width={iconSizeValue}
          height={iconSizeValue}
          style={{ objectFit: 'contain', flexShrink: 0 }}
        />
      );
    }

    return null;
  };

  // Generate tooltip text
  const getTooltipText = (): string => {
    if (typeof tooltip === 'string') return tooltip;
    if (tooltip === false) return '';
    if (presetConfig?.label && text) return `${presetConfig.label}: ${text}`;
    return text || '';
  };

  const tooltipText = getTooltipText();

  const content = (
    <Box
      sx={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: config.gap,
        py: '2px',
        cursor: onClick ? 'pointer' : 'default',
        ...sx,
      }}
      onClick={onClick}
    >
      {renderIcon()}
      <Typography
        sx={{
          fontSize: config.fontSize,
          fontWeight: 400,
          color: textColor || colors.text.secondary,
          maxWidth: config.maxWidth,
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        }}
      >
        {text || '-'}
      </Typography>
    </Box>
  );

  if (tooltip !== false && tooltipText) {
    return (
      <Tooltip title={tooltipText} arrow placement='top'>
        {content}
      </Tooltip>
    );
  }

  return content;
};

// Convenience wrapper for platform channels (backwards compatible)
export const PlatformChannelBadge: React.FC<{
  platform: string;
  channelName: string;
  size?: 'small' | 'medium';
  maxWidth?: number;
}> = ({ platform, channelName, size = 'medium', maxWidth }) => <IconTextBadge preset={platform} text={channelName} size={size} maxWidth={maxWidth} />;

export default IconTextBadge;
