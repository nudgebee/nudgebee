import React, { useState } from 'react';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import { DropdownMenu } from '@ui/DropdownMenu';
import { DeleteIconRed } from '@assets';
import SafeIcon from '@shared/icons/SafeIcon';

interface BaseNodeButton {
  icon: any;
  onClick: (e: React.MouseEvent) => void;
  title: string;
  hoverBackgroundColor: string;
  hoverBorderColor: string;
  show?: boolean;
}

interface NodeContentConfig {
  icon: any;
  label: any;
  description: string;
  badge?: any; // Optional badge (like "Trigger")
  iconContainerStyle?: React.CSSProperties;
  labelStyle?: React.CSSProperties;
  descriptionStyle?: React.CSSProperties;
  statusBadges?: any; // Optional status indicators
}

interface BaseNodeProps {
  // Node content configuration
  content: NodeContentConfig;

  // Additional custom content (like handles, connection lines, etc.)
  additionalContent?: any;

  // Node appearance
  selected?: boolean;
  border?: string; // Border color/style
  borderRadius?: string;
  boxShadow?: string;
  hoverShadow?: string; // Shadow on hover
  minWidth?: string;
  maxWidth?: string;
  minHeight?: string;
  padding?: string;
  background?: string;

  // Additional custom styles (will override defaults)
  nodeStyle?: React.CSSProperties;

  onDelete: () => void;
  primaryButton?: BaseNodeButton;
  menuItems?: Array<{
    label: string;
    onClick: () => void;
    icon?: React.ReactNode;
  }>;
  deleteButtonConfig?: {
    title?: string;
    hidden?: boolean;
  };
}

const BaseNode: React.FC<BaseNodeProps> = ({
  content,
  additionalContent,
  selected = false,
  border,
  borderRadius = 'var(--ds-space-4)',
  boxShadow = '0 5px var(--ds-space-2) color-mix(in srgb, black 20%, transparent)',
  hoverShadow = '0 var(--ds-space-2) var(--ds-space-4) color-mix(in srgb, #959595 70%, transparent)',
  minWidth = '250px',
  maxWidth = '250px',
  minHeight = '80px',
  padding = '14px var(--ds-space-4)',
  background = 'white',
  nodeStyle = {},
  onDelete,
  primaryButton,
  menuItems = [],
  deleteButtonConfig = {},
}) => {
  const [isHovered, setIsHovered] = useState(false);
  const [deleteButtonHovered, setDeleteButtonHovered] = useState(false);
  const [primaryButtonHovered, setPrimaryButtonHovered] = useState(false);
  const [moreButtonHovered, setMoreButtonHovered] = useState(false);
  // Open state is tracked locally only to drive the trigger's open/hover
  // highlight; DropdownMenu owns the actual anchor/positioning.
  const [moreMenuOpen, setMoreMenuOpen] = useState(false);

  // Default styles for icon container
  const defaultIconContainerStyle: React.CSSProperties = {
    height: '32px',
    width: '32px',
    borderRadius: 'var(--ds-radius-lg)',
    display: 'flex',
    gap: 'var(--ds-space-2)',
    alignItems: 'center',
    justifyContent: 'center',
  };

  // Default styles for label
  const defaultLabelStyle: React.CSSProperties = {
    fontWeight: 'bold',
    fontSize: 'var(--ds-text-body-lg)',
    color: 'var(--ds-brand-500)',
  };

  // Default styles for description
  const defaultDescriptionStyle: React.CSSProperties = {
    fontSize: 'var(--ds-text-small)',
    color: 'var(--ds-brand-500)',
    lineHeight: '1.3',
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
    maxWidth: '100%',
  };

  // Build the node container style
  const containerStyle: React.CSSProperties = {
    padding,
    background,
    border: border || (selected ? '3px solid var(--ds-blue-500)' : '1px solid var(--ds-gray-300)'),
    borderRadius,
    boxShadow: isHovered ? hoverShadow : boxShadow,
    minWidth,
    maxWidth,
    minHeight,
    position: 'relative',
    transition: 'all 0.3s ease',
    cursor: isHovered ? 'pointer' : 'grab',
    ...nodeStyle, // Allow override
  };

  return (
    <div onMouseEnter={() => setIsHovered(true)} onMouseLeave={() => setIsHovered(false)} style={containerStyle}>
      {/* Optional Badge (e.g., "Trigger") */}
      {content.badge}

      {/* Node Content - Icon, Label, Description */}
      <div style={{ display: 'flex', alignItems: 'center', overflow: 'hidden', minWidth: 0 }}>
        {/* Text Content */}
        <div style={{ flex: 1, minWidth: 0 }}>
          {/* Label */}
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--ds-space-2)',
              marginBottom: 'var(--ds-space-2)',
              justifyContent: 'space-between',
            }}
          >
            {/* Icon Container */}
            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
              <div
                style={{
                  ...defaultIconContainerStyle,
                  ...content.iconContainerStyle,
                }}
              >
                {content.icon}
              </div>
              <div
                style={{
                  ...defaultLabelStyle,
                  ...content.labelStyle,
                }}
              >
                {content.label}
              </div>
            </div>
            <div>{content.statusBadges}</div>
          </div>
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              gap: 'var(--ds-space-2)',
              padding: 'var(--ds-space-1) var(--ds-space-2)',
              border: '1px solid var(--ds-gray-200)',
              borderRadius: 'var(--ds-radius-lg)',
              height: '100%',
              overflow: 'hidden',
              minWidth: 0,
            }}
          >
            {/* Description */}
            <div
              style={{
                ...defaultDescriptionStyle,
                ...content.descriptionStyle,
              }}
            >
              {content.description}
            </div>
          </div>
        </div>
      </div>

      {/* Additional Content (Handles, Connection Lines, etc.) */}
      {additionalContent}

      {/* Toolbar - Only show on hover */}
      <div
        className='nodrag nopan'
        style={{
          position: 'absolute',
          top: 'calc(var(--ds-space-3) * -1)',
          right: 'var(--ds-space-3)',
          display: 'flex',
          gap: 'var(--ds-space-1)',
          zIndex: 1000,
          pointerEvents: isHovered ? 'auto' : 'none',
          opacity: isHovered ? 1 : 0,
          visibility: isHovered ? 'visible' : 'hidden',
          transition: 'opacity 0.2s ease, visibility 0.2s ease',
        }}
      >
        {/* Delete Button */}
        {!deleteButtonConfig.hidden && (
          <button
            type='button'
            className='nodrag nopan'
            onMouseEnter={() => setDeleteButtonHovered(true)}
            onMouseLeave={() => setDeleteButtonHovered(false)}
            onClick={(e) => {
              e.preventDefault();
              e.stopPropagation();
              onDelete();
            }}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                e.stopPropagation();
                onDelete();
              }
            }}
            tabIndex={0}
            style={{
              background: 'none',
              padding: 0,
              width: '24px',
              height: '24px',
              borderRadius: 'var(--ds-radius-md)',
              backgroundColor: deleteButtonHovered ? 'var(--ds-red-100)' : 'white',
              border: deleteButtonHovered ? '1px solid var(--ds-red-400)' : '1px solid var(--ds-gray-300)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              cursor: 'pointer',
              transition: 'all 0.2s ease-in-out',
              boxShadow: '0 var(--ds-space-0) var(--ds-space-1) var(--ds-gray-alpha-300)',
            }}
            title={deleteButtonConfig.title || 'Delete node'}
          >
            <SafeIcon src={DeleteIconRed} alt='delete' width={14} height={14} style={{ pointerEvents: 'none' }} />
          </button>
        )}

        {/* Primary Action Button (Run/Test) */}
        {primaryButton && primaryButton.show !== false && (
          <button
            type='button'
            className='nodrag nopan'
            onMouseEnter={() => setPrimaryButtonHovered(true)}
            onMouseLeave={() => setPrimaryButtonHovered(false)}
            onClick={(e) => {
              e.preventDefault();
              e.stopPropagation();
              primaryButton.onClick(e);
            }}
            onKeyDown={(e) => {
              if (e.key === 'Enter' || e.key === ' ') {
                e.preventDefault();
                e.stopPropagation();
                primaryButton.onClick(e as any);
              }
            }}
            tabIndex={0}
            style={{
              background: 'none',
              padding: 0,
              width: '24px',
              height: '24px',
              borderRadius: 'var(--ds-radius-md)',
              backgroundColor: primaryButtonHovered ? primaryButton.hoverBackgroundColor : 'white',
              border: primaryButtonHovered ? `1px solid ${primaryButton.hoverBorderColor}` : '1px solid var(--ds-gray-300)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              cursor: 'pointer',
              transition: 'all 0.2s ease-in-out',
              boxShadow: '0 var(--ds-space-0) var(--ds-space-1) var(--ds-gray-alpha-300)',
            }}
            title={primaryButton.title}
          >
            {primaryButton.icon}
          </button>
        )}

        {/* More Options Menu */}
        {menuItems.length > 0 && (
          <DropdownMenu
            disablePortal={false}
            className='nodrag nopan'
            minWidth={72}
            onClose={() => setMoreMenuOpen(false)}
            trigger={
              <button
                type='button'
                className='nodrag nopan'
                onMouseEnter={() => setMoreButtonHovered(true)}
                onMouseLeave={() => setMoreButtonHovered(false)}
                onClick={() => setMoreMenuOpen(true)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.stopPropagation();
                  }
                }}
                tabIndex={0}
                style={{
                  background: 'none',
                  padding: 0,
                  width: '24px',
                  height: '24px',
                  borderRadius: 'var(--ds-radius-md)',
                  backgroundColor: moreButtonHovered || moreMenuOpen ? 'var(--ds-gray-100)' : 'white',
                  border: moreButtonHovered || moreMenuOpen ? '1px solid var(--ds-gray-400)' : '1px solid var(--ds-gray-300)',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  cursor: 'pointer',
                  transition: 'all 0.2s ease-in-out',
                  boxShadow: '0 var(--ds-space-0) var(--ds-space-1) var(--ds-gray-alpha-300)',
                }}
                title='More options'
              >
                <MoreVertIcon sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-brand-400)', pointerEvents: 'none' }} />
              </button>
            }
            items={menuItems.map((item, index) => ({
              id: `basenode-menu-item-${index}`,
              label: item.label,
              icon: item.icon ? (
                <span style={{ display: 'inline-flex', alignItems: 'center', justifyContent: 'center', width: 16, height: 16 }}>{item.icon}</span>
              ) : undefined,
              onSelect: item.onClick,
            }))}
          />
        )}
      </div>
    </div>
  );
};

export default BaseNode;
