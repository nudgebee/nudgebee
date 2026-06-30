import type { ReactNode, CSSProperties } from 'react';

interface FilterOption {
  type: string;
  showAll?: boolean;
  enabled?: boolean;
  onChange?: (e: any) => void;
  label?: string;
  value?: any;
  id?: string;
  onKeyDown?: (e: any) => void;
  error?: boolean;
  helperText?: string;
  component?: ReactNode;
  options?: any[];
  onSelect?: (e: any, v: any) => void;
  minWidth?: string | number;
  onEnter?: (e: any) => void;
  onClear?: () => void;
  limitTags?: number;
  isDisabled?: boolean;
  selected?: any;
  multiple?: boolean;
  grouped?: boolean;
  groupIcon?: (groupKey: string) => ReactNode;
  key?: string;
  maxWidth?: string | number;
  width?: string | number;
  isOptionsLoading?: boolean;
  selectionWithinGroup?: boolean;
}

interface CopyingOption {
  enabled: boolean;
  onClick: (() => void) | null;
}

interface ModalButton {
  enabled: boolean;
  text: string;
  onClick: () => void;
  id: string;
}

interface SharingOptions {
  sharing: {
    enabled: boolean;
    onClick: (() => void) | null;
  };
  download: {
    enabled: boolean;
    onClick: () => { tableId: string } | { canvasId: string | string[] };
  };
}

interface DateTimeRange {
  enabled: boolean;
  onChange: (e: any) => void;
  passedSelectedDateTime: {
    startTime: number;
    endTime: number;
    shortcutClickTime: number;
  };
  shortCuts?: string[];
  showAbsoluteRange?: boolean;
}

interface OnRefresh {
  enabled: boolean;
  text: string;
  onClick: () => void;
  loading: boolean;
}

interface ToggleButtons {
  options: any[];
  activeButton: string;
  handleSelectToggle: () => void;
}

interface SearchOption {
  enabled: boolean;
  placeholder?: string;
  value?: string;
  onChange?: (e: any) => void;
  onClear?: () => void;
  onEnter?: () => void;
  width?: string;
}

interface ShowFiltersOnRightSide {
  [key: string]: any;
}

export interface BoxLayout2Props {
  id?: string;
  showBorder?: boolean;
  heading?: string;
  marginTop?: number | string;
  marginBottom?: string;
  children?: ReactNode;
  filterOptions?: FilterOption[];
  copyingOption?: CopyingOption;
  minDate?: any;
  alphaIcon?: boolean;
  modalButton?: ModalButton;
  customButton?: ReactNode;
  sharingOptions?: SharingOptions;
  extraOptions?: any[];
  leftExtraOptions?: any[];
  dateTimeRange?: DateTimeRange;
  sx?: CSSProperties;
  onRefresh?: OnRefresh;
  toggleButtons?: ToggleButtons;
  searchOption?: SearchOption;
  rowGap?: number | string;
  displaySideFilters?: boolean;
  onClearAll?: () => void;
  resetDateTime?: any;
  showFiltersOnRightSide?: ShowFiltersOnRightSide;
  expandedAccordions?: any;
  setExpandedAccordions?: any;
}

declare const BoxLayout2: React.FC<BoxLayout2Props>;

export default BoxLayout2;
