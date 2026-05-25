/**
 * ThreeDotsMenu (v2) — kebab/overflow trigger backed entirely by DS V2 primitives.
 *
 * Per the DropdownMenu spec, ThreeDotsMenu is not a separate primitive — it's
 * just `<DropdownMenu trigger={<Button composition='icon-only' icon={<MoreVert/>} />} />`.
 * This wrapper preserves the legacy `menuItems[]` / `onMenuClick(item, data)`
 * contract so the dozens of v1 call sites don't all need a per-site change.
 *
 * Item-shape adapter — caller passes the V1 shape:
 *   { id, label, icon: assetSrc, disabled?, reactIcon?, iconBlack?, releaseIcon? }
 * which is mapped to the DS shape `{ id, label, icon: ReactNode, onSelect, disabled }`.
 *   - `icon` (svg asset src) is wrapped in a SafeIcon ReactNode
 *   - `reactIcon` (already a node) is passed straight through
 *   - `releaseIcon` (the sup-badge) is not supported by DropdownMenu and is dropped
 *
 * Submenu (V1 `subMenu`) is intentionally NOT carried over — the DS spec caps
 * nesting at one level. No production call site uses subMenu today.
 */
import React from 'react';
import PropTypes from 'prop-types';
import { MoreVert } from '@mui/icons-material';
import SafeIcon from '@components1/common/SafeIcon';
import { Button as DsButton } from '@components1/ds/Button';
import { DropdownMenu } from '@components1/ds/DropdownMenu';

function resolveIconNode(item) {
  if (item.reactIcon) return item.reactIcon;
  if (item.icon) return <SafeIcon alt={item.label} src={item.icon} height='18' width='18' />;
  return undefined;
}

/**
 * @param {{
 *   id?: string,
 *   sx?: Record<string, any>,
 *   onMenuClick?: (item: any, data?: any) => void,
 *   menuItems?: any[],
 *   data?: any,
 *   lightIcon?: string,
 *   className?: string,
 *   menuWidth?: string | number,
 * }} props
 */
const ThreeDotsMenu = ({
  id,
  onMenuClick,
  menuItems = [],
  data,
  menuWidth,
  sx: _sx = {},
  lightIcon: _lightIcon = '',
  className: _className = '',
}) => {
  if (!menuItems?.length) return null;

  // Adapt v1 items → DS items. Stable per render (cheap mapping); no memo needed.
  const dsItems = menuItems.map((item, index) => ({
    id: item.id != null ? String(item.id) : `tdm-${index}`,
    label: item.label,
    icon: resolveIconNode(item),
    disabled: item.disabled ?? false,
    onSelect: () => {
      if (onMenuClick && data) onMenuClick(item, data);
    },
  }));

  return (
    <DropdownMenu
      align='end'
      side='bottom'
      size='sm'
      minWidth={menuWidth ?? 160}
      items={dsItems}
      trigger={
        <DsButton
          id={id || 'three-dot-menu'}
          tone='secondary'
          size='xs'
          composition='icon-only'
          icon={<MoreVert />}
          aria-label='More actions'
          tooltip='More actions'
        />
      }
    />
  );
};

ThreeDotsMenu.propTypes = {
  id: PropTypes.string,
  sx: PropTypes.object,
  onMenuClick: PropTypes.func,
  menuItems: PropTypes.array,
  data: PropTypes.any,
  lightIcon: PropTypes.string,
  className: PropTypes.string,
  menuWidth: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
};

export default ThreeDotsMenu;
