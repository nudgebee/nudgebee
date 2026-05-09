import React from 'react';

interface FilterDropdownButtonProps {
  id?: string;
  label?: string;
  placeholder?: string;
  options?: any[];
  value?: any;
  multiple?: boolean;
  grouped?: boolean;
  freeSolo?: boolean;
  onSelect?: (event: any, value: any) => void;
  disabled?: boolean;
  isOptionsLoading?: boolean;
  limitTag?: number;
  sx?: object;
  searchPlaceholder?: string;
  required?: boolean;
}

declare const FilterDropdownButton: React.FC<FilterDropdownButtonProps>;
export default FilterDropdownButton;

interface MoreFiltersButtonProps {
  count?: number;
  expanded?: boolean;
  onClick?: () => void;
}

export declare const MoreFiltersButton: React.FC<MoreFiltersButtonProps>;
