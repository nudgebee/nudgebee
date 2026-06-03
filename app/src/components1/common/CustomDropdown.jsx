import * as React from 'react';
import PropTypes from 'prop-types';
import { inputSx, inputCustomSx } from '@data/themes/inputField';
import { Autocomplete, TextField, Paper, InputAdornment, ListItem, Tooltip, CircularProgress } from '@mui/material';
import ClusterStatusIndicator from './ClusterStatusIndicator';
import Link from 'next/link';
import { colors } from 'src/utils/colors';
import { MenuArrowDownIcon } from '@assets';
import Text from './format/Text';
import CloudProviderIcon from './CloudIcon';
import SafeIcon from './SafeIcon';
import { toKebabCase } from 'src/utils/common';

/**
 * @param {{
 *   id?: string,
 *   value?: any,
 *   onChange?: (event: any, value: any) => void,
 *   options?: any[],
 *   label?: string,
 *   minWidth?: string,
 *   isDisabled?: boolean,
 *   disableClearable?: boolean,
 *   align?: string,
 *   showIndicator?: boolean,
 *   showStatusIndicator?: boolean,
 *   headerStyle?: boolean,
 *   clusterData?: any,
 *   noBorder?: boolean,
 *   minHeight?: string | number,
 *   labelFont?: string,
 *   showDynamicPaper?: boolean,
 *   showNormalField?: boolean,
 *   inputVariant?: string,
 *   customStyle?: object,
 *   componentsProps?: object,
 *   additionalAutoCompleteProps?: object,
 *   showBreakWord?: boolean,
 *   isRequired?: boolean,
 *   isLoading?: boolean,
 *   groupByCloudProvider?: boolean,
 *   listboxHeight?: string,
 *   showPadding?: boolean,
 *   error?: boolean,
 *   helperText?: string,
 *   showSmallTopPadding?: boolean,
 *   showAutoEllipsis?: boolean,
 *   openDirection?: 'up' | 'down' | 'auto',
 *   noOptionsText?: string,
 * }} props
 */

const CustomDropdown = ({
  id = '',
  value,
  onChange,
  options = [],
  label = '',
  minWidth = '180px',
  isDisabled = false,
  disableClearable = false,
  align = 'left',
  showIndicator = false,
  showStatusIndicator = false,
  headerStyle = false,
  clusterData = {},
  noBorder = false,
  minHeight = 'auto',
  labelFont = '',
  showDynamicPaper = false,
  showNormalField = false,
  inputVariant = 'outlined',
  customStyle = {},
  componentsProps = {},
  additionalAutoCompleteProps = {},
  showBreakWord = false,
  isRequired = false,
  isLoading = false,
  groupByCloudProvider = false,
  listboxHeight = '400px',
  showPadding = false,
  error = false,
  helperText = '',
  showSmallTopPadding = true,
  showAutoEllipsis = false,
  openDirection = 'auto',
  noOptionsText = 'No options available',
}) => {
  const dropdownRef = React.useRef(null);
  const [dynamicDirection, setDynamicDirection] = React.useState('down');

  const calculateDirection = React.useCallback(() => {
    if (openDirection !== 'auto') {
      setDynamicDirection(openDirection);
      return;
    }

    if (!dropdownRef.current) {
      return;
    }

    const rect = dropdownRef.current.getBoundingClientRect();
    const listboxHeightPx = parseInt(listboxHeight, 10) || 400;
    const spaceBelow = window.innerHeight - rect.bottom;
    const spaceAbove = rect.top;

    // Add some buffer (20px) to account for margins/padding
    if (spaceBelow < listboxHeightPx + 20 && spaceAbove > spaceBelow) {
      setDynamicDirection('up');
    } else {
      setDynamicDirection('down');
    }
  }, [openDirection, listboxHeight]);

  const CustomPaper = (props) => {
    const paperStyle = align == 'left' ? { left: 0 } : { right: 0 };
    const positionStyle = dynamicDirection === 'up' ? { bottom: '40px' } : { top: '5px' };

    return (
      <Paper
        sx={{
          width: '100%',
          overflowY: 'auto',
          overflowX: 'hidden',
          boxSizing: 'border-box',
          position: !showDynamicPaper && 'absolute',
          ...paperStyle,
          ...positionStyle,
          // Match ds/FilterDropdown's overlay surface tokens so all dropdown
          // surfaces share the same chrome (no border, soft shadow, larger radius,
          // overlay-bg). The `headerStyle` flag is preserved as the on-switch for
          // these chrome overrides — non-header callsites keep MUI defaults.
          ...(headerStyle && {
            border: 'none',
            borderRadius: 'var(--ds-overlay-radius)',
            backgroundColor: 'var(--ds-overlay-bg)',
            boxShadow: 'var(--ds-overlay-shadow)',
          }),
          padding: showPadding ? '8px 8px 8px 8px' : '0px',
          // Match the ds/FilterDropdown open animation so all overlay surfaces
          // share the same motion vocabulary. Origin pinned to the side closer
          // to the trigger so the scale reads as "expanding out of the field".
          transformOrigin: dynamicDirection === 'up' ? 'bottom left' : 'top left',
          animation: 'customDropdownSlideIn var(--ds-overlay-enter-duration, 180ms) var(--ds-overlay-enter-easing, cubic-bezier(0.16, 1, 0.3, 1))',
          '@keyframes customDropdownSlideIn': {
            '0%': {
              opacity: 0,
              transform: dynamicDirection === 'up' ? 'scaleY(0.9) translateY(8px)' : 'scaleY(0.9) translateY(-8px)',
            },
            '100%': { opacity: 1, transform: 'scaleY(1) translateY(0)' },
          },
          '.MuiAutocomplete-listbox': {
            padding: '0px',
            overflowX: 'hidden',
            boxSizing: 'border-box',
            '&::-webkit-scrollbar': {
              width: '4px !important',
            },
            '&::-webkit-scrollbar-track': {
              backgroundColor: colors.background.transparent,
            },
          },
          wordBreak: showBreakWord && 'break-word',
        }}
        elevation={8}
        {...props}
      />
    );
  };

  const groupedOptions = React.useMemo(() => {
    if (!groupByCloudProvider) {
      return options;
    }

    // Define provider order: AWS, Azure, GCP, K8s, OCI, then others alphabetically
    const getProviderOrder = (provider) => {
      const normalizedProvider = provider.toLowerCase();
      if (normalizedProvider === 'aws') {
        return 0;
      }
      if (normalizedProvider === 'azure') {
        return 1;
      }
      if (normalizedProvider === 'gcp') {
        return 2;
      }
      if (normalizedProvider === 'k8s') {
        return 3;
      }
      if (normalizedProvider === 'oci') {
        return 4;
      }
      if (normalizedProvider === 'cloudfoundry') {
        return 5;
      }
      return 999; // Other providers at the end alphabetically
    };

    const groups = {};

    options.forEach((option) => {
      const provider = option.cloud_provider || 'Other';
      if (!groups[provider]) {
        groups[provider] = {
          label: provider,
          options: [],
          isGroup: true,
        };
      }
      groups[provider].options.push(option);
    });

    // Helper function to check detailed connection status (from ClusterStatusIndicator logic)
    const isConnectedUsingDate = (lastConnectedDateStr) => {
      if (!lastConnectedDateStr) {
        return false;
      }
      // If last connected is more than 2 days ago, mark it as disconnected
      const lastConnectedDate = new Date(lastConnectedDateStr);
      return new Date().getTime() - lastConnectedDate.getTime() < 2 * 24 * 3600 * 1000;
    };

    const checkConnections = (clusterData) => {
      if (clusterData.cloud_provider?.toLowerCase() != 'k8s') {
        const connectionStatus = clusterData.agent?.connection_status;

        if (!connectionStatus) {
          return clusterData.agent?.status === 'CONNECTED';
        }

        const servicesStatus = {
          events: isConnectedUsingDate(connectionStatus?.events?.end),
          resources: isConnectedUsingDate(connectionStatus?.resources?.updated_at),
          recommendations: isConnectedUsingDate(connectionStatus?.recommendations?.updated_at),
          spends: isConnectedUsingDate(connectionStatus?.spends?.updated_at),
        };

        return Object.values(servicesStatus).every((status) => status === true);
      }

      const requiredProps = ['logsConnection', 'nodeAgentConnection', 'opencostConnection', 'prometheusConnection', 'relayConnection'];

      for (const prop of requiredProps) {
        if (!clusterData.agent?.connection_status[prop]) {
          return false;
        }
      }

      return true;
    };

    // Sort accounts by connection status (green > yellow > red), then alphabetically within each cloud provider group
    Object.values(groups).forEach((group) => {
      group.options.sort((a, b) => {
        // Get connection status for both options
        // 0 = green (fully connected), 1 = yellow (partial), 2 = red (not connected)
        const getConnectionPriority = (option) => {
          if (option.agent?.status === 'CONNECTED') {
            return checkConnections(option) ? 0 : 1; // Green or Yellow
          }
          return 2; // Red
        };

        const aPriority = getConnectionPriority(a);
        const bPriority = getConnectionPriority(b);

        // Sort by connection priority first (lower number = higher priority)
        if (aPriority !== bPriority) {
          return aPriority - bPriority;
        }

        // Then sort alphabetically by label
        const labelA = (a.label || '').toString().toLowerCase();
        const labelB = (b.label || '').toString().toLowerCase();
        return labelA.localeCompare(labelB, undefined, { numeric: true, sensitivity: 'base' });
      });
    });

    // Sort groups by provider order: AWS, Azure, GCP, K8s
    return Object.values(groups)
      .sort((a, b) => {
        const orderA = getProviderOrder(a.label);
        const orderB = getProviderOrder(b.label);
        return orderA - orderB;
      })
      .flatMap((group) => [group, ...group.options]);
  }, [options, groupByCloudProvider]);

  const handleChange = (_event, v) => {
    if (!onChange || (groupByCloudProvider && v?.isGroup)) {
      return;
    }

    const newEventObj = {
      target: {
        value: v?.value ?? v,
      },
    };
    onChange(newEventObj, v);
  };

  const getOptionLabel = (option) => {
    if (typeof option === 'string') {
      return option;
    }
    if (React.isValidElement(option.label)) {
      return '';
    }
    if (groupByCloudProvider && option.isGroup) {
      return option.label;
    }
    return option.label || '';
  };

  const isOptionEqualToValue = (option, value) => {
    // If value is null, undefined, or an empty string, nothing is selected.
    if (value == null || value === '') {
      return false;
    }

    if (typeof option === 'string') {
      return option === value;
    }

    // Group headers should not be considered selectable.
    if (option.isGroup) {
      return false;
    }

    // Ensure we compare the actual unique identifiers.
    const optionVal = option?.value;
    const selectedVal = value && typeof value === 'object' ? value.value : value;

    return optionVal === selectedVal;
  };

  const groupedOptionStyles = {
    backgroundColor: `${colors.background.primaryLightest} !important`,
    borderRadius: 'var(--ds-radius-sm)',
    borderLeft: `3px solid ${colors.border.primaryLight} !important`,
  };

  const renderOption = (props, option) => {
    const { key, ...propsWithoutKey } = props;

    if (groupByCloudProvider && option.isGroup) {
      return (
        <ListItem
          key={key}
          {...propsWithoutKey}
          sx={{
            fontWeight: 'var(--ds-font-weight-regular)',
            pointerEvents: 'none',
            fontSize: 'var(--ds-text-small)',
            fontFamily: 'Roboto',
            marginTop: 'var(--ds-space-1) !important',
            marginBottom: 'var(--ds-space-1) !important',
            color: colors.text.secondary,
            borderBottom: '1px solid',
            borderImage:
              'linear-gradient(to right, rgb(223, 223, 223) 0%, rgb(223, 223, 223) 30%, rgba(223, 223, 223, 0.6) 70%, rgba(223, 223, 223, 0.3) 90%, transparent 100%) 1',
            padding: 'var(--ds-space-1) var(--ds-space-1) !important',
          }}
        >
          <CloudProviderIcon cloud_provider={option.label} height='16px' width='16px' />
          <Text
            value={option.label === 'K8s' ? 'K8s clusters' : option.label}
            showAutoEllipsis
            sx={{
              fontWeight: 'var(--ds-font-weight-semibold)',
              pointerEvents: 'none',
              borderRadius: 'var(--ds-radius-sm)',
              fontSize: 'var(--ds-text-caption)',
              fontFamily: 'Poppins',
              letterSpacing: '-0.1px',
              color: colors.text.secondary,
              padding: 'var(--ds-space-1) var(--ds-space-2)',
            }}
          />
        </ListItem>
      );
    }

    return (
      <ListItem
        key={key}
        {...propsWithoutKey}
        sx={{
          padding: headerStyle && '0px !important',
          fontSize: groupByCloudProvider && '14px',
          fontFamily: groupByCloudProvider && 'Roboto',
          color: colors.text.secondary,
          fontWeight: groupByCloudProvider && 400,
          borderRadius: 'var(--ds-radius-lg) !important',
          ml: groupByCloudProvider && '6px',
          border: groupByCloudProvider && `0.5px solid ${colors.border.white}`,
          pl: groupByCloudProvider ? 4 : 2,
          boxSizing: 'border-box',
          maxWidth: '96%',
          ...(groupByCloudProvider && {
            // Hover/focus aligned with ds/FilterDropdown overlay-item tokens;
            // selected-state keeps the original left-border accent (per spec).
            transition: 'background var(--ds-motion-micro) var(--ds-motion-ease)',
            '&:hover': {
              color: 'var(--ds-gray-700)',
              backgroundColor: 'var(--ds-overlay-item-hover-bg)',
            },
            '&[aria-selected="true"]': {
              ...groupedOptionStyles,
              color: colors.text.secondary,
              '& *': {
                fontWeight: 'var(--ds-font-weight-medium) !important',
              },
            },
            '&[aria-selected="true"]:hover': {
              color: colors.text.secondary,
              fontWeight: 'var(--ds-font-weight-semibold) !important',
              backgroundColor: colors.background.primaryLightest,
            },
            '&.Mui-focused': {
              color: 'var(--ds-gray-700)',
              backgroundColor: 'var(--ds-overlay-item-hover-bg)',
            },
          }),
          ...(!groupByCloudProvider && {
            borderBottom: headerStyle && `1px solid ${colors.border.vertical}`,
          }),
        }}
      >
        {showIndicator && <ClusterStatusIndicator showBorder={false} clusterData={option} />}
        {option?.icon && (
          <SafeIcon
            src={option.icon}
            alt={option?.label || ''}
            style={{ height: '18px', width: '18px', marginRight: 'var(--ds-space-2)', flexShrink: 0 }}
          />
        )}
        {React.isValidElement(option?.label) ? (
          <span style={{ display: 'flex', width: '100%' }}>{option.label}</span>
        ) : (
          <span style={{ display: 'flex', width: '100%' }}>
            <Text
              sx={{ fontSize: groupByCloudProvider && '12px' }}
              value={option?.label ?? option}
              showAutoEllipsis={showAutoEllipsis}
              placement='left'
            />
          </span>
        )}
      </ListItem>
    );
  };

  const inputId = toKebabCase(id || label || '');

  return (
    <Autocomplete
      ref={dropdownRef}
      size='medium'
      key={`auto-complete-${label}`}
      id={inputId ? `auto-complete-${inputId}` : 'auto-complete'}
      disableClearable={disableClearable}
      onOpen={calculateDirection}
      popupIcon={
        <SafeIcon
          src={MenuArrowDownIcon}
          alt='dropdown arrow'
          style={{
            height: '18px',
            width: '18px',
            opacity: '80%',
            transition: 'transform 0.3s ease',
          }}
        />
      }
      blurOnSelect='mouse'
      componentsProps={{ ...componentsProps }}
      sx={{
        ...inputCustomSx,
        width: minWidth || 150,
        minHeight: '36px',
        border: !noBorder ? 'none' : '',
        '.MuiOutlinedInput-root.MuiInputBase-sizeSmall': {
          minHeight: minHeight,
        },
        '.css-xfmy6f-MuiFormLabel-root-MuiInputLabel-root': {
          fontSize: `${labelFont} !important`,
        },
        '& .MuiOutlinedInput-root': {
          paddingLeft: showStatusIndicator && '0px !important',
          paddingTop: showStatusIndicator && showSmallTopPadding && '7px !important',
        },
        ...customStyle,
        ...(isDisabled && {
          '& .MuiOutlinedInput-root': {
            backgroundColor: colors.background.input,
            borderColor: colors.border.secondary,
            pointerEvents: 'none',
            '.MuiOutlinedInput-notchedOutline': {
              borderColor: colors.border.secondary,
            },
          },
          '& .MuiInputBase-input.Mui-disabled': {
            color: colors.text.disabledInput,
          },
        }),
      }}
      value={options?.find((option) => option.value == value) || value || null}
      options={groupByCloudProvider ? groupedOptions : options}
      onChange={handleChange}
      disabled={isDisabled}
      noOptionsText={noOptionsText}
      renderInput={(params) => (
        <TextField
          {...params}
          label={label}
          margin='normal'
          sx={!showNormalField && inputSx}
          variant={inputVariant}
          size='small'
          id={`auto-complete-input-${label}`}
          required={isRequired}
          error={error}
          helperText={error ? helperText : ''}
          InputProps={{
            ...params.InputProps,
            startAdornment:
              showStatusIndicator && clusterData ? (
                <InputAdornment position='start'>
                  <Link passHref href={`/agentHealth?accountId=${clusterData?.value}#agent`}>
                    <Tooltip title='Cluster Health'>
                      <span style={{ display: 'inline-block', cursor: 'pointer' }}>
                        <ClusterStatusIndicator showBorder={true} clusterData={clusterData} />
                      </span>
                    </Tooltip>
                  </Link>
                  <CloudProviderIcon
                    sx={{ paddingLeft: 2 }}
                    cloud_provider={options.find((opt) => opt.value === value)?.cloud_provider || clusterData.cloud_provider || 'K8S'}
                    height={'14px'}
                    width={'14px'}
                  />
                </InputAdornment>
              ) : null,
            endAdornment: (
              <>
                {isLoading ? <CircularProgress size={20} /> : null}
                {params.InputProps.endAdornment}
              </>
            ),
          }}
        />
      )}
      renderOption={renderOption}
      isOptionEqualToValue={isOptionEqualToValue}
      getOptionLabel={getOptionLabel}
      PaperComponent={CustomPaper}
      ListboxProps={{
        style: {
          maxHeight: listboxHeight,
        },
      }}
      {...additionalAutoCompleteProps}
    />
  );
};

CustomDropdown.propTypes = {
  height: PropTypes.any,
  options: PropTypes.array.isRequired,
  value: PropTypes.any,
  onChange: PropTypes.func,
  label: PropTypes.string,
  minWidth: PropTypes.string,
  noBorder: PropTypes.bool,
  isDisabled: PropTypes.bool,
  disableClearable: PropTypes.bool,
  minHeight: PropTypes.any,
  align: PropTypes.string,
  showStatusIndicator: PropTypes.bool,
  showIndicator: PropTypes.bool,
  headerStyle: PropTypes.bool,
  clusterData: PropTypes.any,
  labelFont: PropTypes.string,
  showDynamicPaper: PropTypes.bool,
  showNormalField: PropTypes.bool,
  inputVariant: PropTypes.string,
  customStyle: PropTypes.any,
  componentsProps: PropTypes.any,
  additionalAutoCompleteProps: PropTypes.any,
  id: PropTypes.string,
  showBreakWord: PropTypes.bool,
  isRequired: PropTypes.bool,
  isLoading: PropTypes.bool,
  groupByCloudProvider: PropTypes.bool,
  listboxHeight: PropTypes.string,
  showPadding: PropTypes.bool,
  error: PropTypes.bool,
  helperText: PropTypes.string,
  noOptionsText: PropTypes.string,
};

export default React.memo(CustomDropdown);
