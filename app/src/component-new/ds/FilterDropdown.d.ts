import React from 'react';

// Type shadow for the DS FilterDropdown.jsx — the runtime component is a JS
// file with PropTypes only. Without this declaration TypeScript infers the
// `options`/`value` types from the default param values (`options = []`) and
// narrows them to `never[]`, breaking every typed call site.
interface FilterDropdownProps {
  id?: string;
  label?: string;
  placeholder?: string;
  options?: any[];
  value?: any;
  multiple?: boolean;
  grouped?: boolean;
  groupIcon?: (groupKey: string) => React.ReactNode;
  freeSolo?: boolean;
  onSelect?: (event: any, value: any) => void;
  disabled?: boolean;
  isOptionsLoading?: boolean;
  limitTag?: number;
  size?: 'sm' | 'md' | 'lg';
  sx?: object;
  searchPlaceholder?: string;
  required?: boolean;
  selectionWithinGroup?: boolean;
}

declare const FilterDropdown: React.FC<FilterDropdownProps>;
export default FilterDropdown;

interface MoreFiltersButtonProps {
  count?: number;
  expanded?: boolean;
  onClick?: () => void;
}

export declare const MoreFiltersButton: React.FC<MoreFiltersButtonProps>;
