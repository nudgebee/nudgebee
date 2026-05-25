import React, { useState, useRef, useEffect } from 'react';
import {
  Box,
  TextField,
  Paper,
  Typography,
  List,
  ListItem,
  ListItemText,
  Chip,
  InputAdornment,
  IconButton,
  Tooltip,
  Popover,
  CircularProgress,
} from '@mui/material';
import {
  Label as LabelIcon,
  FilterList as FilterIcon,
  Search as SearchIcon,
  Close as CloseIcon,
  Info as InfoIcon,
  Storage as ValueIcon,
} from '@mui/icons-material';
import { snackbar } from '@components1/common/snackbarService';
import { parseRelayHttpResponseBodyMessage } from 'src/utils/common';
import observability from '@api1/observability';

const SigNozQueryAutocomplete = ({ accountId, onQueryChange, queryItems }) => {
  const [inputValue, setInputValue] = useState('');
  const [suggestions, setSuggestions] = useState([]);
  const [showSuggestions, setShowSuggestions] = useState(false);
  const [selectedIndex, setSelectedIndex] = useState(-1);
  const [chips, setChips] = useState(queryItems || []);
  const [currentStep, setCurrentStep] = useState('label'); // 'label', 'operator', 'value'
  const [pendingChip, setPendingChip] = useState({ label: '', operator: '', value: '' });
  const [labels, setLabels] = useState([]);
  const [values, setValues] = useState({}); // Cache for label values
  const [loadingValues, setLoadingValues] = useState(false);
  const [infoAnchorEl, setInfoAnchorEl] = useState(null);

  const inputRef = useRef(null);
  const suggestionsRef = useRef(null);
  const chipIdCounter = useRef(0);

  useEffect(() => {
    observability
      .fetchLogLabels({
        account_id: accountId,
        log_provider: 'signoz',
      })
      .then((res) => {
        const responseAttributes = res?.data?.data?.logs_list_labels || [];
        if (responseAttributes.length > 0) {
          setLabels(responseAttributes);
        }
      });
  }, []);

  const mockOperators = [
    { value: '=', label: '= (equals)', description: 'Exact match' },
    { value: '!=', label: '!= (not equals)', description: 'Not equal to' },
    { value: 'in', label: 'IN (in list)', description: 'Matches any value in the list' },
    { value: 'nin', label: 'NOT IN (not in list)', description: 'Does not match any value in the list' },
    { value: 'like', label: 'LIKE (pattern match)', description: 'Matches using SQL LIKE pattern (e.g. %value%)' },
    { value: 'nlike', label: 'NOT LIKE (not pattern match)', description: 'Does not match SQL LIKE pattern' },
    { value: 'contains', label: 'CONTAINS', description: 'Checks if value contains a substring' },
  ];

  // Fetch values for a specific label
  const fetchValuesForLabel = async (labelName) => {
    if (values[labelName]) {
      return values[labelName]; // Return cached values
    }

    setLoadingValues(true);
    try {
      const findLabel = labels.find((l) => l.label === labelName);
      if (findLabel) {
        const response = await observability.fetchLogLabelValues({
          account_id: accountId,
          log_provider: 'signoz',
          request: {
            attributeKey: findLabel.label,
            filterAttributeKeyDataType: findLabel?.attributes?.dataType || 'string',
            searchText: '',
            tagType: findLabel?.attributes?.type || 'resource',
          },
        });

        const labelValues = response?.data?.data?.logs_list_label_values?.map((d) => d.value) || [];
        if (labelValues?.length > 0) {
          // Cache the values
          setValues((prev) => ({
            ...prev,
            [labelName]: labelValues,
          }));
          return labelValues;
        }
        snackbar.error(`Failed to fetch values- ${parseRelayHttpResponseBodyMessage(response)}`);
      }
    } catch (error) {
      console.error('Error fetching values for label:', labelName, error);
    } finally {
      setLoadingValues(false);
    }
    return [];
  };

  const getAvailableLabels = () => {
    // Allow all labels to be used multiple times
    // If wanted to filter already selected label const usedLabels = new Set(chips.map((chip) => chip.label));
    return labels.map((g) => g.label);
  };

  const filterSuggestions = (suggestions, filter) => {
    if (!filter) {
      return suggestions;
    }
    return suggestions.filter(
      (suggestion) =>
        suggestion.value.toLowerCase().includes(filter.toLowerCase()) ||
        (suggestion.label && suggestion.label.toLowerCase().includes(filter.toLowerCase()))
    );
  };

  const getDisplayValue = () => {
    return inputValue;
  };

  const handleInputChange = async (e) => {
    const value = e.target.value;

    // Check if user manually deleted everything - reset to label step
    if (!value.trim()) {
      setInputValue('');
      setPendingChip({ label: '', operator: '', value: '' });
      setCurrentStep('label');
      setSuggestions([]);
      setShowSuggestions(false);
      setSelectedIndex(-1);
      return;
    }

    setInputValue(value);

    // Parse the input to detect step automatically
    const parts = value.trim().split(/\s+/);

    if (parts.length === 1) {
      // Only one word - could be label
      setCurrentStep('label');
      setPendingChip({ label: '', operator: '', value: '' });

      // Check if the typed label exists in available labels
      const availableLabels = getAvailableLabels();
      const matchingLabel = availableLabels.find((label) => label.toLowerCase() === parts[0].toLowerCase());

      if (matchingLabel) {
        // Exact match found - show operators immediately
        setPendingChip({ label: matchingLabel, operator: '', value: '' });
        setCurrentStep('operator');

        const operatorSuggestions = mockOperators.map((op) => ({
          type: 'operator',
          value: op.value,
          label: op.label,
          description: op.description,
        }));
        setSuggestions(operatorSuggestions);
        setShowSuggestions(true);
        setSelectedIndex(-1);
        return;
      }

      // Show label suggestions
      const allLabels = getAvailableLabels().map((label) => ({ type: 'label', value: label, label }));
      const filtered = filterSuggestions(allLabels, parts[0]);
      setSuggestions(filtered);
      setShowSuggestions(filtered.length > 0);
      setSelectedIndex(-1);
    } else if (parts.length === 2) {
      // Two parts - label + operator (or partial operator)
      const [label, operatorPart] = parts;
      setCurrentStep('operator');
      setPendingChip({ label, operator: '', value: '' });

      // Check if operator is complete
      const matchingOperator = mockOperators.find((op) => op.value.toLowerCase() === operatorPart.toLowerCase());

      if (matchingOperator) {
        // Complete operator found, move to value step and fetch values
        setPendingChip({ label, operator: matchingOperator.value, value: '' });
        setCurrentStep('value');

        // Fetch and show value suggestions
        try {
          const labelValues = await fetchValuesForLabel(label);
          const valueSuggestions = labelValues.map((val) => ({
            type: 'value',
            value: val,
            label: val,
          }));
          setSuggestions(valueSuggestions);
          setShowSuggestions(valueSuggestions.length > 0);
        } catch {
          setSuggestions([]);
          setShowSuggestions(false);
        }
        setSelectedIndex(-1);
        return;
      }

      // Show operator suggestions
      const operatorSuggestions = mockOperators.map((op) => ({
        type: 'operator',
        value: op.value,
        label: op.label,
        description: op.description,
      }));
      const filtered = filterSuggestions(operatorSuggestions, operatorPart);
      setSuggestions(filtered);
      setShowSuggestions(filtered.length > 0);
      setSelectedIndex(-1);
    } else if (parts.length >= 3) {
      // Three or more parts - label + operator + value
      const [label, operator, ...valueParts] = parts;
      const valueStr = valueParts.join(' ');
      setCurrentStep('value');
      setPendingChip({ label, operator, value: valueStr });

      // Show filtered value suggestions based on current input
      if (values[label]) {
        const valueSuggestions = values[label].map((val) => ({
          type: 'value',
          value: val,
          label: val,
        }));
        const filtered = filterSuggestions(valueSuggestions, valueStr);
        setSuggestions(filtered);
        setShowSuggestions(filtered.length > 0);
        setSelectedIndex(-1);
      } else {
        // Try to fetch values if not cached
        fetchValuesForLabel(label).then((labelValues) => {
          const valueSuggestions = labelValues.map((val) => ({
            type: 'value',
            value: val,
            label: val,
          }));
          const filtered = filterSuggestions(valueSuggestions, valueStr);
          setSuggestions(filtered);
          setShowSuggestions(filtered.length > 0);
          setSelectedIndex(-1);
        });
      }
    }
  };

  const handleInputFocus = async () => {
    if (!inputValue.trim()) {
      // Show label suggestions when input is empty
      const allLabels = getAvailableLabels().map((label) => ({ type: 'label', value: label, label }));
      setSuggestions(allLabels);
      setShowSuggestions(allLabels.length > 0);
      setSelectedIndex(-1);
    } else {
      // Re-parse current input to show appropriate suggestions
      const parts = inputValue.trim().split(/\s+/);

      if (parts.length === 1 && currentStep === 'label') {
        const allLabels = getAvailableLabels().map((label) => ({ type: 'label', value: label, label }));
        const filtered = filterSuggestions(allLabels, parts[0]);
        setSuggestions(filtered);
        setShowSuggestions(filtered.length > 0);
        setSelectedIndex(-1);
      } else if (parts.length === 2 && currentStep === 'operator') {
        const operatorSuggestions = mockOperators.map((op) => ({
          type: 'operator',
          value: op.value,
          label: op.label,
          description: op.description,
        }));
        const filtered = filterSuggestions(operatorSuggestions, parts[1]);
        setSuggestions(filtered);
        setShowSuggestions(filtered.length > 0);
        setSelectedIndex(-1);
      } else if (currentStep === 'value' && pendingChip.label) {
        // Show value suggestions for the current label
        try {
          const labelValues = await fetchValuesForLabel(pendingChip.label);
          const valueSuggestions = labelValues.map((val) => ({
            type: 'value',
            value: val,
            label: val,
          }));

          // Filter by current value input if any
          const currentValue = parts.length >= 3 ? parts.slice(2).join(' ') : '';
          const filtered = filterSuggestions(valueSuggestions, currentValue);
          setSuggestions(filtered);
          setShowSuggestions(filtered.length > 0);
          setSelectedIndex(-1);
        } catch {
          setSuggestions([]);
          setShowSuggestions(false);
        }
      }
    }
  };

  useEffect(() => {
    const query = generateQuery();
    if (onQueryChange) {
      onQueryChange(query);
    }
  }, [chips]);

  const handleKeyDown = (e) => {
    // Handle Enter key to create chip from free text input
    if (e.key === 'Enter') {
      e.preventDefault();

      if (!inputValue.trim()) {
        return;
      }

      const parts = inputValue.trim().split(/\s+/);

      if (parts.length >= 3) {
        // Complete query: label operator value
        const [label, operator, ...valueParts] = parts;
        const value = valueParts.join(' ');

        // Validate operator exists in mockOperators
        const validOperator = mockOperators.find((op) => op.value === operator);
        if (validOperator) {
          const newChip = { label, operator, value, id: chipIdCounter.current++ };
          setChips([...chips, newChip]);
          setInputValue('');
          setPendingChip({ label: '', operator: '', value: '' });
          setCurrentStep('label');
          setShowSuggestions(false);

          setTimeout(() => {
            inputRef.current?.focus();
          }, 0);
          return;
        }
      }

      // If there are suggestions visible, select the first one
      if (showSuggestions && suggestions.length > 0) {
        const suggestionToSelect = selectedIndex >= 0 ? suggestions[selectedIndex] : suggestions[0];
        handleSuggestionSelect(suggestionToSelect);
        return;
      }
    }

    if (!showSuggestions) {
      return;
    }

    if (e.key === 'ArrowDown') {
      e.preventDefault();
      setSelectedIndex((prev) => (prev < suggestions.length - 1 ? prev + 1 : 0));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      setSelectedIndex((prev) => (prev > 0 ? prev - 1 : suggestions.length - 1));
    } else if (e.key === 'Tab') {
      e.preventDefault();
      if (selectedIndex >= 0) {
        handleSuggestionSelect(suggestions[selectedIndex]);
      }
    } else if (e.key === 'Escape') {
      setShowSuggestions(false);
      setSelectedIndex(-1);
    }
  };

  const handleSuggestionSelect = async (suggestion) => {
    setShowSuggestions(false);
    setSelectedIndex(-1);

    if (suggestion.type === 'label') {
      setPendingChip({ label: suggestion.value, operator: '', value: '' });
      setInputValue(suggestion.value + ' ');
      setCurrentStep('operator');

      // Show operator suggestions immediately
      setTimeout(() => {
        const operatorSuggestions = mockOperators.map((op) => ({
          type: 'operator',
          value: op.value,
          label: op.label,
          description: op.description,
        }));
        setSuggestions(operatorSuggestions);
        setShowSuggestions(true);
        setSelectedIndex(-1);
      }, 0);
    } else if (suggestion.type === 'operator') {
      const newInputValue = pendingChip.label + ' ' + suggestion.value + ' ';
      setPendingChip({ ...pendingChip, operator: suggestion.value });
      setInputValue(newInputValue);
      setCurrentStep('value');

      // Fetch and show value suggestions
      try {
        const labelValues = await fetchValuesForLabel(pendingChip.label);
        const valueSuggestions = labelValues.map((val) => ({
          type: 'value',
          value: val,
          label: val,
        }));
        setSuggestions(valueSuggestions);
        setShowSuggestions(valueSuggestions.length > 0);
        setSelectedIndex(-1);
      } catch {
        setSuggestions([]);
        setShowSuggestions(false);
      }

      // Set cursor to the end of the input
      setTimeout(() => {
        if (inputRef.current) {
          inputRef.current.focus();
          inputRef.current.setSelectionRange(newInputValue.length, newInputValue.length);
        }
      }, 0);
      return;
    } else if (suggestion.type === 'value') {
      // When value is selected, immediately create a chip
      const newChip = {
        label: pendingChip.label,
        operator: pendingChip.operator,
        value: suggestion.value,
        id: chipIdCounter.current++,
      };

      setChips([...chips, newChip]);
      setInputValue('');
      setPendingChip({ label: '', operator: '', value: '' });
      setCurrentStep('label');
      setSuggestions([]);
      setShowSuggestions(false);
      setSelectedIndex(-1);

      setTimeout(() => {
        inputRef.current?.focus();
      }, 0);
      return;
    }

    setTimeout(() => {
      inputRef.current?.focus();
    }, 0);
  };

  const handleChipDelete = (chipId) => {
    setChips(chips.filter((chip) => chip.id !== chipId));
  };

  const handleInfoClick = (event) => {
    setInfoAnchorEl(event.currentTarget);
  };

  const handleInfoClose = () => {
    setInfoAnchorEl(null);
  };

  const isInfoOpen = Boolean(infoAnchorEl);

  const getPlaceholder = () => {
    switch (currentStep) {
      case 'label':
        return 'Type label name or select from suggestions.';
      case 'operator':
        return `Type operator for "${pendingChip.label}" or select from suggestions.`;
      case 'value':
        return `Type value for "${pendingChip.label} ${pendingChip.operator}" or select from suggestions.`;
      default:
        return 'Type query (e.g., "service.name = my-api") or use suggestions.';
    }
  };

  const getStepIcon = () => {
    if (loadingValues && currentStep === 'value') {
      return <CircularProgress size={20} />;
    }

    switch (currentStep) {
      case 'label':
        return <LabelIcon />;
      case 'operator':
        return <FilterIcon />;
      case 'value':
        return <ValueIcon />;
      default:
        return <SearchIcon />;
    }
  };

  const generateQuery = () => {
    if (chips.length === 0) {
      return [];
    }

    return chips.map((chip) => {
      let value = chip.value;

      // For 'in' and 'nin' operators, convert value to array
      if (chip.operator === 'in' || chip.operator === 'nin') {
        // Split by comma and trim whitespace, handle both comma-separated and single values
        value = chip.value
          .split(',')
          .map((v) => v.trim())
          .filter((v) => v.length > 0);
        // If only one value after splitting, still keep it as array
        if (value.length === 0 && chip.value.trim()) {
          value = [chip.value.trim()];
        }
      }

      return {
        key: {
          key: chip.label,
        },
        value: value,
        op: chip.operator,
      };
    });
  };

  useEffect(() => {
    const handleClickOutside = (event) => {
      if (suggestionsRef.current && !suggestionsRef.current.contains(event.target) && inputRef.current && !inputRef.current.contains(event.target)) {
        setShowSuggestions(false);
        // Don't reset selectedIndex here to maintain state
      }
    };

    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  return (
    <Box sx={{ width: '100%', mx: 'auto', minHeight: '20vh' }}>
      {/* Input Field with Info Icon */}
      <Box sx={{ position: 'relative', display: 'flex', alignItems: 'flex-start', gap: 1, width: '55%' }}>
        <TextField
          inputRef={inputRef}
          fullWidth
          value={getDisplayValue()}
          onChange={handleInputChange}
          onKeyDown={handleKeyDown}
          onFocus={handleInputFocus}
          placeholder={getPlaceholder()}
          disabled={false}
          label='Write Query'
          variant='outlined'
          InputProps={{
            startAdornment: <InputAdornment position='start'>{getStepIcon()}</InputAdornment>,
            sx: {
              fontFamily: 'monospace',
              fontSize: '0.875rem',
            },
          }}
          sx={{
            '& .MuiOutlinedInput-root': {
              '& fieldset': {
                borderColor: 'grey.300',
              },
              '&:hover fieldset': {
                borderColor: 'primary.main',
              },
              '&.Mui-focused fieldset': {
                borderColor: 'primary.main',
              },
            },
          }}
        />

        <Tooltip title='How to use'>
          <IconButton
            onClick={handleInfoClick}
            sx={{
              mt: 1,
              color: 'primary.main',
              '&:hover': {
                bgcolor: 'primary.light',
                color: 'primary.contrastText',
              },
            }}
          >
            <InfoIcon />
          </IconButton>
        </Tooltip>

        {/* Suggestions Dropdown */}
        {showSuggestions && suggestions.length > 0 && (
          <Paper
            ref={suggestionsRef}
            elevation={8}
            sx={{
              position: 'absolute',
              top: '100%',
              left: 0,
              right: 0,
              zIndex: 10,
              mt: 1,
              maxHeight: 300,
              overflow: 'auto',
            }}
          >
            <List disablePadding>
              {suggestions.map((suggestion, index) => (
                <ListItem
                  key={`${suggestion.type}-${suggestion.value}`}
                  button
                  selected={index === selectedIndex}
                  onClick={() => handleSuggestionSelect(suggestion)}
                  onMouseEnter={() => setSelectedIndex(index)}
                  sx={{
                    py: 1.5,
                    borderBottom: index < suggestions.length - 1 ? 1 : 0,
                    borderColor: 'divider',
                    '&.Mui-selected': {
                      bgcolor: 'primary.light',
                      color: 'primary.contrastText',
                      borderLeft: 4,
                      borderLeftColor: 'primary.main',
                    },
                    '&:hover': {
                      bgcolor: 'grey.100',
                    },
                  }}
                >
                  <Box sx={{ mr: 2 }}>
                    {suggestion.type === 'label' && <LabelIcon color='success' />}
                    {suggestion.type === 'operator' && <FilterIcon color='warning' />}
                    {suggestion.type === 'value' && <ValueIcon color='primary' />}
                  </Box>
                  <ListItemText
                    primary={suggestion.value}
                    secondary={suggestion.description}
                    primaryTypographyProps={{
                      fontFamily: 'monospace',
                      fontSize: '0.875rem',
                      fontWeight: 'medium',
                    }}
                    secondaryTypographyProps={{
                      fontSize: '0.75rem',
                    }}
                  />
                </ListItem>
              ))}
            </List>
          </Paper>
        )}
      </Box>

      {chips.length > 0 && (
        <Box sx={{ mt: 3, width: '70%' }}>
          <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 1, alignItems: 'center' }}>
            {chips.slice(0, 2).map((chip) => (
              <Chip
                key={chip.id}
                icon={<LabelIcon />}
                label={`${chip.label} ${chip.operator} ${chip.value}`}
                onDelete={() => handleChipDelete(chip.id)}
                deleteIcon={<CloseIcon />}
                color='primary'
                sx={{
                  fontFamily: 'monospace',
                  '& .MuiChip-label': { fontSize: '0.75rem' },
                }}
              />
            ))}
            {chips.length > 2 && (
              <Tooltip
                title={
                  <Box
                    sx={{
                      maxHeight: '300px',
                      overflowY: 'auto',
                      overflowX: 'hidden',
                      '&::-webkit-scrollbar': {
                        width: '6px',
                      },
                      '&::-webkit-scrollbar-track': {
                        backgroundColor: 'rgba(255, 255, 255, 0.1)',
                        borderRadius: '3px',
                      },
                      '&::-webkit-scrollbar-thumb': {
                        backgroundColor: 'rgba(255, 255, 255, 0.3)',
                        borderRadius: '3px',
                        '&:hover': {
                          backgroundColor: 'rgba(255, 255, 255, 0.5)',
                        },
                      },
                    }}
                  >
                    {chips.slice(2).map((chip, index) => (
                      <Box
                        key={chip.id}
                        sx={{
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'space-between',
                          backgroundColor: 'rgba(255, 255, 255, 0.1)',
                          borderRadius: '4px',
                          padding: '4px 8px',
                          mb: index < chips.slice(2).length - 1 ? 0.5 : 0,
                          border: '1px solid rgba(255, 255, 255, 0.2)',
                        }}
                      >
                        <Typography
                          variant='caption'
                          sx={{
                            fontFamily: 'monospace',
                            fontSize: '0.75rem',
                            flex: 1,
                            wordBreak: 'break-word',
                          }}
                        >
                          {`${chip.label} ${chip.operator} ${chip.value}`}
                        </Typography>
                        <IconButton
                          size='small'
                          onClick={(e) => {
                            e.stopPropagation();
                            handleChipDelete(chip.id);
                          }}
                          sx={{
                            ml: 1,
                            width: '16px',
                            height: '16px',
                            color: 'rgba(255, 255, 255, 0.7)',
                            flexShrink: 0,
                            '&:hover': {
                              color: 'white',
                              backgroundColor: 'rgba(255, 255, 255, 0.1)',
                            },
                          }}
                        >
                          <CloseIcon sx={{ fontSize: '12px' }} />
                        </IconButton>
                      </Box>
                    ))}
                  </Box>
                }
                arrow
                placement='top'
                componentsProps={{
                  tooltip: {
                    sx: {
                      backgroundColor: 'rgba(97, 97, 97, 0.92)',
                      color: 'white',
                      maxWidth: '400px',
                      maxHeight: '350px',
                      padding: '12px',
                    },
                  },
                  popper: {
                    modifiers: [
                      {
                        name: 'preventOverflow',
                        options: {
                          boundary: 'viewport',
                          padding: 8,
                        },
                      },
                      {
                        name: 'flip',
                        options: {
                          fallbackPlacements: ['bottom', 'left', 'right'],
                        },
                      },
                    ],
                  },
                }}
              >
                <Chip
                  label={`+${chips.length - 2}`}
                  color='secondary'
                  variant='outlined'
                  sx={{
                    fontFamily: 'monospace',
                    cursor: 'help',
                    '& .MuiChip-label': {
                      fontSize: '0.75rem',
                      fontWeight: 'bold',
                    },
                  }}
                />
              </Tooltip>
            )}
          </Box>
        </Box>
      )}

      {/* Info Popover */}
      <Popover
        open={isInfoOpen}
        anchorEl={infoAnchorEl}
        onClose={handleInfoClose}
        anchorOrigin={{
          vertical: 'bottom',
          horizontal: 'right',
        }}
        transformOrigin={{
          vertical: 'top',
          horizontal: 'right',
        }}
        PaperProps={{
          sx: { maxWidth: 400, p: 2 },
        }}
      >
        <Typography variant='subtitle2' gutterBottom sx={{ fontWeight: 'medium' }}>
          How to use:
        </Typography>
        <Box component='ul' sx={{ m: 0, pl: 2, '& li': { fontSize: '0.75rem', color: 'text.secondary', mb: 0.5 } }}>
          <li>Type freely: &quot;service.name = my-api&quot; or use suggestions step by step</li>
          <li>If you type an exact label name, operators will appear automatically</li>
          <li>After selecting an operator, values for that label will be suggested</li>
          <li>Press Enter to create a filter from complete query (label operator value)</li>
          <li>Each completed filter becomes a chip that you can delete</li>
          <li>Use arrow keys to navigate suggestions, Enter/Tab to select, Escape to close</li>
          <li>Multiple filters are combined with AND logic</li>
          <li>The selected label/operator will appear in the input box as you build</li>
        </Box>
      </Popover>
    </Box>
  );
};

export default SigNozQueryAutocomplete;
