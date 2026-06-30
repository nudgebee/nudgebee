import React, { type ReactElement, useState } from 'react';
import { Tooltip, styled, tooltipClasses, type TooltipProps, Box, Typography } from '@mui/material';
import { InfoOutlined } from '@mui/icons-material';
import { colors } from 'src/utils/colors';
import CustomButton from './NewCustomButton';

/**
 * CustomTooltip - A styled tooltip component with multiple variants
 *
 * Variants:
 * - 'default': Simple tooltip with just the title text. Compact padding (8px 12px).
 *
 * - 'explainer': Informational tooltip with title (header) and optional desc (body). Shows an info icon.
 *               Larger padding (12px 16px). Use for explaining features or concepts.
 *
 * - 'interactive': Tooltip with title (header), optional desc (body), and a clickable link/button.
 *                  Stays open when hovering over the tooltip content. Use when you need user interaction.
 *                  Requires linkUrl and linkText props.
 *
 * Props:
 * - title: The main text (simple text for default, header text for explainer/interactive)
 * - desc: Optional description text shown below the title (for explainer/interactive variants)
 * - linkUrl/linkText: Required for interactive variant to show a button
 */

type TooltipVariant = 'default' | 'explainer' | 'interactive';

// Modern white tooltip with border, shadow, and arrow styling
const StyledTooltip = styled(
  ({
    className,
    tooltipStyle: _tooltipStyle,
    variant: _variant,
    maxWidth: _maxWidth,
    ...props
  }: TooltipProps & {
    tooltipStyle?: React.CSSProperties;
    variant?: TooltipVariant;
    maxWidth?: string;
  }) => <Tooltip {...props} classes={{ popper: className }} />
)(({ tooltipStyle, variant, maxWidth }: { tooltipStyle?: React.CSSProperties; variant?: TooltipVariant; maxWidth?: string }) => {
  // Adjust padding based on variant
  const getPadding = () => {
    switch (variant) {
      case 'explainer':
        return '12px 16px';
      case 'interactive':
        return '12px 16px';
      case 'default':
      default:
        return '8px 12px';
    }
  };

  return {
    [`& .${tooltipClasses.tooltip}`]: {
      backgroundColor: 'var(--ds-background-100)',
      color: 'rgb(30, 41, 59)',
      border: '1px solid rgb(164, 192, 235)',
      boxShadow: '0px 6px 10px rgba(0, 0, 0, 0.1)',
      borderRadius: 'var(--ds-radius-lg)',
      padding: getPadding(),
      fontSize: 'var(--ds-text-small)',
      margin: 'var(--ds-space-6)',
      fontWeight: 'var(--ds-font-weight-regular)',
      lineHeight: '1.5',
      maxWidth: maxWidth ?? '250px',
      width: 'fit-content',
      wordBreak: 'break-word',
      boxSizing: 'border-box',
      ...tooltipStyle,
    },
    // Style the arrow to match the white tooltip with border
    [`& .${tooltipClasses.arrow}`]: {
      color: 'var(--ds-background-100)',
      '&::before': {
        backgroundColor: 'var(--ds-background-100)',
        border: '1px solid rgb(164, 192, 235)',
        boxShadow: '0px 2px 4px rgba(0, 0, 0, 0.06)',
      },
    },
  };
});

// Content wrapper for different variants
const TooltipContent: React.FC<{
  variant: TooltipVariant;
  title: React.ReactNode;
  desc?: React.ReactNode;
  linkUrl?: string;
  linkText?: string;
}> = ({ variant, title, desc, linkUrl, linkText }) => {
  if (variant === 'explainer') {
    return (
      <Box>
        <Box
          sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--ds-space-1)', gap: 'var(--ds-space-1)' }}
        >
          <Typography
            variant='body2'
            sx={{
              fontWeight: 'var(--ds-font-weight-semibold)',
              fontSize: 'var(--ds-text-body)',
              color: colors.text.secondary,
            }}
          >
            {title}
          </Typography>
          <InfoOutlined
            sx={{
              fontSize: 'var(--ds-text-title)',
              color: colors.text.tertiarymedium,
            }}
          />
        </Box>
        {desc && (
          <Typography
            variant='body2'
            sx={{
              fontWeight: 'var(--ds-font-weight-regular)',
              fontSize: 'var(--ds-text-small)',
              color: 'var(--ds-brand-500)',
              lineHeight: '1.4',
              marginTop: 'var(--ds-space-1)',
            }}
          >
            {desc}
          </Typography>
        )}
      </Box>
    );
  }

  if (variant === 'interactive' && linkUrl && linkText) {
    return (
      <Box>
        <Box
          sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--ds-space-1)', gap: 'var(--ds-space-1)' }}
        >
          <Typography
            variant='body2'
            sx={{
              fontWeight: 'var(--ds-font-weight-semibold)',
              fontSize: 'var(--ds-text-body)',
              color: colors.text.secondary,
            }}
          >
            {title}
          </Typography>
          <InfoOutlined
            sx={{
              fontSize: 'var(--ds-text-title)',
              color: colors.text.tertiarymedium,
            }}
          />
        </Box>
        {desc && (
          <Typography
            variant='body2'
            sx={{
              fontWeight: 'var(--ds-font-weight-regular)',
              fontSize: 'var(--ds-text-small)',
              color: 'var(--ds-brand-500)',
              marginTop: 'var(--ds-space-1)',
              marginBottom: 'var(--ds-space-2)',
              lineHeight: '1.4',
            }}
          >
            {desc}
          </Typography>
        )}
        <CustomButton
          variant='tertiary'
          size='xSmall'
          text={linkText}
          sx={{
            fontSize: 'var(--ds-text-caption)',
            fontWeight: 'var(--ds-font-weight-regular)',
            cursor: 'pointer',
            height: '22px',
            marginTop: 'var(--ds-space-1)',
          }}
          onClick={(e: React.MouseEvent) => {
            e.stopPropagation();
            window.open(linkUrl, '_blank', 'noopener,noreferrer');
          }}
        />
      </Box>
    );
  }

  // Default variant
  return (
    <Box
      sx={{
        maxHeight: '400px',
        overflow: 'auto',
        '&::-webkit-scrollbar': { width: '3px' },
        '&::-webkit-scrollbar-track': { background: 'transparent' },
        '&::-webkit-scrollbar-thumb': { background: 'rgba(0,0,0,0.15)', borderRadius: 'var(--ds-radius-sm)' },
        scrollbarWidth: 'thin',
        scrollbarColor: 'rgba(0,0,0,0.15) transparent',
      }}
    >
      {title}
    </Box>
  );
};

interface CustomTooltipProps extends Omit<TooltipProps, 'children' | 'title' | 'content'> {
  children: ReactElement<any>;
  variant?: TooltipVariant;
  /** The tooltip content (for default variant) or title text (for explainer/interactive variants) */
  title: React.ReactNode;
  /** Description text shown below the title (for explainer/interactive variants) */
  desc?: React.ReactNode;
  tooltipStyle?: React.CSSProperties;
  tooltipClassName?: string;
  // Props for "interactive" variant
  linkUrl?: string;
  linkText?: string;
}

// Extract plain text length from React nodes (handles JSX with string children)
const getTextLength = (node: React.ReactNode): number => {
  if (typeof node === 'string') return node.length;
  if (typeof node === 'number') return String(node).length;
  if (React.isValidElement(node)) {
    const children = (node.props as { children?: React.ReactNode }).children;
    if (children) return getTextLength(children);
  }
  if (Array.isArray(node)) return node.reduce((sum, child) => sum + getTextLength(child), 0);
  return 0;
};

// Compute maxWidth based on content length
const getAutoMaxWidth = (title: React.ReactNode, desc: React.ReactNode): string => {
  const textLength = getTextLength(title) + getTextLength(desc);

  if (textLength === 0) return '300px';

  return textLength > 200 ? '550px' : '300px';
};

const CustomTooltip = React.forwardRef<HTMLDivElement, CustomTooltipProps>(
  ({ title, desc, children, variant = 'default', placement = 'top', tooltipStyle = {}, tooltipClassName = '', linkUrl, linkText, ...rest }, ref) => {
    const [open, setOpen] = useState(false);

    if (!React.isValidElement(children)) {
      return null;
    }

    if (!title && !desc) {
      return <>{children}</>;
    }

    // For interactive tooltips, we need to keep them open when hovering over the tooltip itself
    const isInteractive = variant === 'interactive';

    const tooltipContent = <TooltipContent variant={variant} title={title} desc={desc} linkUrl={linkUrl} linkText={linkText} />;

    const resolvedMaxWidth = (tooltipStyle.maxWidth as string) ?? getAutoMaxWidth(title, desc);

    const handleOpen = () => setOpen(true);
    const handleClose = () => setOpen(false);

    const tooltipProps: any = {
      placement,
      tooltipStyle,
      variant,
      maxWidth: resolvedMaxWidth,
      arrow: true,
      classes: { tooltip: tooltipClassName },
      PopperProps: {
        modifiers: [
          {
            name: 'flip',
            enabled: true,
            options: {
              fallbackPlacements: ['bottom', 'right', 'left'],
            },
          },
          {
            name: 'preventOverflow',
            enabled: true,
            options: {
              boundary: 'viewport',
              altAxis: true,
              padding: 8,
            },
          },
        ],
      },
      ...rest,
    };

    if (isInteractive) {
      // For interactive tooltips, control open state manually
      tooltipProps.open = open;
      tooltipProps.onOpen = handleOpen;
      tooltipProps.onClose = handleClose;
      tooltipProps.disableFocusListener = false;
      tooltipProps.disableHoverListener = false;
      tooltipProps.disableTouchListener = false;
      // Allow interaction with tooltip content
      tooltipProps.componentsProps = {
        tooltip: {
          sx: {
            pointerEvents: 'auto',
          },
          onMouseEnter: handleOpen,
          onMouseLeave: handleClose,
        },
      };
    }

    return (
      <StyledTooltip title={tooltipContent} {...tooltipProps}>
        {React.cloneElement(children as ReactElement<any>, {
          ref,
          ...(isInteractive && {
            onMouseEnter: handleOpen,
            onMouseLeave: handleClose,
          }),
        })}
      </StyledTooltip>
    );
  }
);

CustomTooltip.displayName = 'CustomTooltip';

export default CustomTooltip;
