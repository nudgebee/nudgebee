/**
 * ListingLayout — DS V2 shell for table-listing screens (recommendations,
 * inventory pages, audit views, etc.).
 * Spec: design-system/primitives/layout/listing-layout.html
 *
 * Owns ONLY the shell: card chrome, toolbar styling, body padding,
 * footer divider. Does NOT know about filter types, action buttons, or
 * pagination — pages compose those primitives directly into the slots.
 *
 * Anatomy:
 *   <ListingLayout id="...">
 *     <ListingLayout.Toolbar title="...">
 *       <FilterDropdown ... />        // left side
 *       <FilterDropdown ... />
 *       <ListingLayout.ToolbarSpacer />
 *       <DsButton ... />              // right side
 *       <DsAutoRefresh ... />
 *     </ListingLayout.Toolbar>
 *     <ListingLayout.Body>
 *       <DsTable ... />
 *     </ListingLayout.Body>
 *     <ListingLayout.Footer>
 *       <DsPagination ... />
 *     </ListingLayout.Footer>
 *   </ListingLayout>
 *
 * Why slots not props:
 *   The legacy BoxLayout2 took a polymorphic `filterOptions` array and 12+
 *   feature-flag props (searchOption, toggleButtons, dateTimeRange,
 *   sharingOptions, modalButton, customButton, …). Every new toolbar widget
 *   required editing the wrapper. Slots let pages compose any DS primitive
 *   without growing this file's API surface.
 *
 * Don't:
 *   - Don't put the page-level Stat summary cards inside ListingLayout — they
 *     live as siblings above. The shell is for the table card only.
 *   - Don't paginate inside Body. Pagination is a Footer-slot primitive per
 *     Table spec.
 */
import * as React from 'react';
import { Box, Typography, type SxProps, type Theme } from '@mui/material';
import { ds } from 'src/utils/colors';
import WidgetCard from './WidgetCard';
import Tooltip from './Tooltip';

export interface ListingLayoutProps {
  id?: string;
  children: React.ReactNode;
  sx?: SxProps<Theme>;
}

export interface ListingLayoutToolbarProps {
  /** Section heading. Omit when the page already has an outer tab/title bar above the card. */
  title?: React.ReactNode;
  /** Filter widgets (FilterDropdown, SearchInput, Chip groups). Rendered left-aligned after the title. */
  children?: React.ReactNode;
  /** Right-aligned action cluster (download menu, auto-refresh, primary button). */
  actions?: React.ReactNode;
  id?: string;
  'data-testid'?: string;
  sx?: SxProps<Theme>;
}

export interface ListingLayoutBodyProps {
  children: React.ReactNode;
  /** Inner padding around the body. Default `0 ds.space[5]`. Pass `0` for edge-to-edge tables. */
  padding?: string | number;
  id?: string;
  sx?: SxProps<Theme>;
}

export type ListingLayoutFooterAlign = 'start' | 'end' | 'between';

export interface ListingLayoutFooterProps {
  children: React.ReactNode;
  /** Justify-content. Default 'end' (right-aligned, the Pagination convention). */
  align?: ListingLayoutFooterAlign;
  sx?: SxProps<Theme>;
}

function ToolbarTitle({ title }: { title: React.ReactNode }) {
  const ref = React.useRef<HTMLSpanElement>(null);
  const [truncated, setTruncated] = React.useState(false);

  const checkOverflow = () => {
    if (ref.current) setTruncated(ref.current.scrollWidth > ref.current.clientWidth);
  };

  return (
    <Tooltip title={truncated ? title : ''} placement='top'>
      <Typography
        ref={ref}
        onMouseEnter={checkOverflow}
        sx={{
          fontSize: ds.text.body,
          fontWeight: ds.weight.semibold,
          color: ds.gray[700],
          mr: ds.space[2],
          flexShrink: 0,
          lineHeight: '32px',
          maxWidth: '320px',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        }}
      >
        {title}
      </Typography>
    </Tooltip>
  );
}

function Toolbar({ title, children, actions, id, sx, ...rest }: ListingLayoutToolbarProps) {
  // Two-cluster layout: the outer flex row never wraps, so `actions` is always
  // pinned to the right edge of row 1. The left cluster is the only thing that
  // wraps — when many filters overflow they stack underneath the first row of
  // filters, but the right-aligned actions stay glued to the top-right.
  //
  // Cap the toolbar's height at one row (currently — see the v2 backlog item
  // "Toolbar: +N more filters overflow") by giving the left cluster a horizontal
  // scroll if it grows too tall. For now we let it wrap so nothing is hidden.
  return (
    <Box
      id={id}
      sx={{
        display: 'flex',
        alignItems: 'flex-start',
        gap: ds.space[2],
        padding: `${ds.space[4]} ${ds.space[5]}`,
        backgroundColor: ds.background[100],
        borderTopLeftRadius: '12px',
        borderTopRightRadius: '12px',
        ...sx,
      }}
      {...rest}
    >
      {title && <ToolbarTitle title={title} />}
      {/* Left cluster — wraps internally when filters overflow. */}
      <Box
        sx={{
          flex: '1 1 auto',
          minWidth: 0,
          display: 'flex',
          flexWrap: 'wrap',
          alignItems: 'center',
          gap: ds.space[2],
        }}
      >
        {children}
      </Box>
      {/* Right cluster — never wraps to a new row; pinned to top-right. */}
      {actions && (
        <Box
          sx={{
            flexShrink: 0,
            display: 'flex',
            alignItems: 'center',
            gap: ds.space[2],
          }}
        >
          {actions}
        </Box>
      )}
    </Box>
  );
}

function ToolbarSpacer() {
  // Escape hatch when callers want explicit control over where the spacer
  // sits (e.g. multiple right-aligned clusters). The Toolbar already inserts
  // one automatic spacer between `children` and `actions`.
  return <Box sx={{ flex: 1, minWidth: 0 }} />;
}

function Body({ children, padding = `0 ${ds.space[5]}`, id, sx }: ListingLayoutBodyProps) {
  return (
    <Box id={id} sx={{ padding, ...sx }}>
      {children}
    </Box>
  );
}

function Footer({ children, align = 'end', sx }: ListingLayoutFooterProps) {
  const justify = align === 'start' ? 'flex-start' : align === 'between' ? 'space-between' : 'flex-end';
  return (
    <Box
      sx={{
        display: 'flex',
        justifyContent: justify,
        alignItems: 'center',
        padding: `${ds.space[3]} ${ds.space[4]}`,
        borderTop: `1px solid ${ds.gray[200]}`,
        ...sx,
      }}
    >
      {children}
    </Box>
  );
}

export function ListingLayout({ id, children, sx }: ListingLayoutProps) {
  return (
    <WidgetCard
      id={id}
      sx={{
        mt: 0,
        mb: 4,
        padding: 0,
        ...sx,
      }}
    >
      {children}
    </WidgetCard>
  );
}

ListingLayout.Toolbar = Toolbar;
ListingLayout.ToolbarSpacer = ToolbarSpacer;
ListingLayout.Body = Body;
ListingLayout.Footer = Footer;

export default ListingLayout;
