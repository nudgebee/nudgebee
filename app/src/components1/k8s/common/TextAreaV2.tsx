import CustomButton from '@components1/common/NewCustomButton';
import TextareaAutosize, { type TextareaAutosizeProps } from '@mui/material/TextareaAutosize';
import { Avatar, Box, ClickAwayListener, Popper, styled, Typography } from '@mui/material';
import type { Theme } from '@mui/material/styles';
import React, { useEffect, useRef, useState } from 'react';
import { ArrowRightWhiteIcon, CustomAgentBlueIcon } from '@assets';
import { colors } from 'src/utils/colors';
import { getIcon } from '@components1/llm/common/AgentIcon';
import StopIcon from '@mui/icons-material/Stop';
import ArrowDropDownIcon from '@mui/icons-material/ArrowDropDown';
import SafeIcon from '@components1/common/SafeIcon';

const blue = {
  100: '#DAECFF',
  200: '#b6daff',
  400: '#3399FF',
  500: '#007FFF',
  600: '#0072E5',
  900: '#003A75',
};

const grey = {
  50: '#F3F6F9',
  100: '#E5EAF2',
  200: '#DAE2ED',
  300: '#C7D0DD',
  400: '#B0B8C4',
  500: '#9DA8B7',
  600: '#6B7A90',
  700: '#434D5B',
  800: '#303740',
  900: '#1C2025',
};

// Define custom props interface
interface CustomTextareaProps extends TextareaAutosizeProps {
  fontSize?: string;
  fontWeight?: string;
  width?: string;
  theme?: Theme;
  maxRows?: number;
}

export const Textarea = styled(TextareaAutosize, { shouldForwardProp: (prop) => prop !== 'fontSize' && prop !== 'maxRows' })<CustomTextareaProps>(
  ({ fontSize = '0.875rem', fontWeight = '400', width = '500px', maxRows = 5 }) => `
    box-sizing: border-box;
    width: ${width};
    font-family: "Roboto", sans-serif;
    font-size:  ${fontSize};
    font-weight: ${fontWeight};
    line-height: 1.5;
    padding: 8px 12px;
    border-radius: 8px;
    color: ${grey[900]};
    background: #fff;
    border: 1px solid ${grey[200]};
    box-shadow: 0px 2px 2px ${grey[50]};
    max-height: calc(${maxRows} * 1.5em + 16px);
    overflow-y: auto !important;
    resize: vertical;
    &:hover {
      border-color: ${blue[400]};
    }
  
    &:focus {
      border-color: ${blue[400]};
      box-shadow: 0 0 0 3px ${blue[200]};
    }
  
    // firefox
    &:focus-visible {
      outline: 0;
    }

    &::-webkit-scrollbar {
      width: 6px;
      display: none;
    }

    &:hover::-webkit-scrollbar {
      display: block;
    }

    &::-webkit-scrollbar-track {
      border-radius: 4px;
      background-color: ${grey[200]};
    }

    &::-webkit-scrollbar-thumb {
      background-color: ${grey[400]};
      border-radius: 4px;
    }

    &::-webkit-scrollbar-thumb:hover {
      background-color: ${grey[500]};
    }
  `
);

interface ModelOption {
  provider: string;
  model: string;
  source?: string;
}

interface AutoSuggestTextareaProps {
  value: string;
  suggestionsAt: { name: string; display_name: string }[];
  functionSuggestions?: { name: string; description: string; variables?: any; variable_defaults?: any }[];
  placeholder: string;
  maxRows: number;
  maxLength: number;
  onKeyDown: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
  fontSize: string;
  fontWeight: string;
  onClick: () => void;
  buttonProperties: {
    show: boolean;
    enable: boolean;
    onClick: (e: string, config?: { llm_provider?: string; llm_model_name?: string }) => void;
    onClickStop: () => void;
  };
  chatScreen?: boolean;
  isFollowUp?: boolean;
  disabled?: boolean;
  allowStop?: boolean;
  models?: ModelOption[];
  defaultModel?: { provider: string; model: string };
  selectedModel?: ModelOption | null;
  onModelSelect?: (model: ModelOption | null) => void;
  popupInitial?: boolean;
}

const AutoSuggestTextarea: React.FC<AutoSuggestTextareaProps> = ({
  value,
  suggestionsAt,
  functionSuggestions = [],
  placeholder,
  maxLength,
  maxRows,
  onKeyDown,
  fontSize,
  fontWeight,
  buttonProperties,
  chatScreen = false,
  isFollowUp = false,
  disabled = false,
  allowStop = false,
  models = [],
  defaultModel: _defaultModel,
  selectedModel,
  onModelSelect,
  popupInitial = false,
}) => {
  const [text, setText] = useState('');
  const [showSuggestions, setShowSuggestions] = useState(false);
  const [anchorEl, setAnchorEl] = useState<null | HTMLElement>(null);
  const [filteredSuggestions, setFilteredSuggestions] = useState<{ name: string; display_name: string }[]>([]);
  const [filteredFunctions, setFilteredFunctions] = useState<{ name: string; description: string; variables?: any; variable_defaults?: any }[]>([]);
  const [selectedIndex, setSelectedIndex] = useState(-1);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const [suggestionsTrigger, setSuggestionsTrigger] = useState<'at' | 'button' | 'call'>('at');
  const [selectedAgent, setSelectedAgent] = useState<string | null>(null);
  const [showModelDropdown, setShowModelDropdown] = useState(false);
  const [modelAnchorEl, setModelAnchorEl] = useState<null | HTMLElement>(null);
  const agentButtonRef = useRef<HTMLDivElement | null>(null);
  const modelButtonRef = useRef<HTMLDivElement | null>(null);

  const buildFunctionCall = (selectedFunction: { name: string; variables?: any; variable_defaults?: any }) => {
    let functionCall = `/call ${selectedFunction.name}`;
    if (selectedFunction.variables && selectedFunction.variables.length > 0) {
      const paramPairs = selectedFunction.variables.map((variable: string) => {
        const defaultValue = selectedFunction.variable_defaults?.[variable] || '';
        return `${variable}="${defaultValue}"`;
      });
      functionCall += ` ${paramPairs.join(' ')}`;
    }
    return functionCall;
  };

  const handleChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const value = e.target.value;
    setText(value);

    // Handle @agent suggestions
    const atMatch = /^@(\w+)/.exec(value);
    const typedAgent = atMatch ? atMatch[1].trim().toLowerCase() : '';
    const matchedAgents = suggestionsAt.filter(
      (suggest) => suggest.name.toLowerCase().startsWith(typedAgent) || suggest.display_name.toLowerCase().startsWith(typedAgent)
    );

    // Handle /call function suggestions
    const callMatch = /^\/call(?:\s+(\w*))?/.exec(value);
    const typedFunction = callMatch && callMatch[1] ? callMatch[1].trim().toLowerCase() : '';
    const matchedFunctions = functionSuggestions.filter((func) => func.name.toLowerCase().startsWith(typedFunction));

    // Check if function name is complete and parameters are present
    const hasCompleteFunction = /^\/call\s+(\w+)(?:\s+\w+="[^"]*")*/.test(value);
    const hasParametersInText = /^\/call\s+\w+\s+\w+="/.test(value);

    if (value.startsWith('@') && suggestionsAt.length > 0 && matchedAgents.length > 0) {
      setSuggestionsTrigger('at');
      setFilteredSuggestions(matchedAgents);
      setFilteredFunctions([]);
      setShowSuggestions(true);
      setSelectedIndex(-1);
      const isSuggestionPresent = matchedAgents.some(
        (suggest) => suggest.name.toLowerCase() === typedAgent || suggest.display_name.toLowerCase() === typedAgent
      );
      if (isSuggestionPresent) {
        setShowSuggestions(false);
      }
      setAnchorEl(textareaRef.current);
    } else if (value.startsWith('/call') && functionSuggestions.length > 0) {
      setSuggestionsTrigger('call');
      setFilteredSuggestions([]);

      // Only show suggestions if function name is not complete or has no parameters yet
      if (!hasCompleteFunction || (typedFunction !== '' && !hasParametersInText)) {
        const functionsToShow = typedFunction === '' ? functionSuggestions : matchedFunctions;
        setFilteredFunctions(functionsToShow);
        setShowSuggestions(true);
        setSelectedIndex(-1);
        setAnchorEl(textareaRef.current);
      } else {
        setShowSuggestions(false);
      }
    } else {
      setShowSuggestions(false);
    }
  };

  const handleSelectSuggestion = (suggest: string) => {
    if (suggestionsTrigger === 'at') {
      const atIndex = text.indexOf('@');
      if (atIndex !== -1) {
        const beforeAt = text.substring(0, atIndex);
        const afterAtPattern = text.substring(atIndex).match(/^@\w*/);
        const afterAtEnd = afterAtPattern ? atIndex + afterAtPattern[0].length : atIndex + 1;
        const afterReplacement = text.substring(afterAtEnd);
        setText(beforeAt + `@${suggest}` + afterReplacement);
      } else {
        setText(`@${suggest} `);
      }
      setSelectedAgent(suggest);
    } else if (suggestionsTrigger === 'button') {
      setText(`@${suggest} `);
      setSelectedAgent(suggest);
    } else if (suggestionsTrigger === 'call') {
      // Find the selected function details
      const selectedFunc = filteredFunctions.find((func) => func.name === suggest);
      if (selectedFunc) {
        const callIndex = text.indexOf('/call');
        if (callIndex !== -1) {
          const beforeCall = text.substring(0, callIndex);
          const afterCallPattern = text.substring(callIndex).match(/^\/call\s*\w*/);
          const afterCallEnd = afterCallPattern ? callIndex + afterCallPattern[0].length : callIndex + 5;
          const afterReplacement = text.substring(afterCallEnd);
          setText(beforeCall + buildFunctionCall(selectedFunc) + afterReplacement);
        } else {
          setText(buildFunctionCall(selectedFunc) + ' ');
        }
      }
    }
    setShowSuggestions(false);
    setSelectedIndex(-1);
    setTimeout(() => {
      textareaRef.current?.focus();
    }, 0);
  };

  useEffect(() => {
    setText(value);
  }, [value]);

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (showSuggestions) {
      const currentList = suggestionsTrigger === 'call' ? filteredFunctions : filteredSuggestions;
      switch (e.key) {
        case 'ArrowDown':
          e.preventDefault();
          setSelectedIndex((prev) => (prev < currentList.length - 1 ? prev + 1 : 0));
          break;
        case 'ArrowUp':
          e.preventDefault();
          setSelectedIndex((prev) => (prev > 0 ? prev - 1 : currentList.length - 1));
          break;
        case 'Enter':
          e.preventDefault();
          if (selectedIndex >= 0) {
            if (suggestionsTrigger === 'call') {
              handleSelectSuggestion(filteredFunctions[selectedIndex].name);
            } else {
              handleSelectSuggestion(filteredSuggestions[selectedIndex].name);
            }
            return;
          }
          break;
        case 'Escape':
          setShowSuggestions(false);
          setSelectedIndex(-1);
          break;
      }
    }
    onKeyDown?.(e);
  };

  const clearSelectedAgent = () => {
    if (selectedAgent) {
      setText('');
    }
    setSelectedAgent(null);
  };

  const handleButtonClick = () => {
    if (selectedAgent) {
      clearSelectedAgent();
    } else {
      setSuggestionsTrigger('button');
      setFilteredSuggestions(suggestionsAt);
      setShowSuggestions(!showSuggestions);
      setAnchorEl(agentButtonRef.current || textareaRef.current);
      setSelectedIndex(-1);
      setTimeout(() => {
        textareaRef.current?.focus();
      }, 0);
    }
  };

  useEffect(() => {
    if (text.startsWith('@')) {
      const match = text.match(/^@(\w+)/);
      if (match) {
        const typedAgent = match[1];
        const filteredValue = suggestionsAt.find((suggest) => suggest.name === typedAgent);
        if (filteredValue) {
          setSelectedAgent(typedAgent);
        }
      }
    } else if (selectedAgent) {
      setSelectedAgent(null);
    }
  }, [text, suggestionsAt]);

  return (
    <Box sx={{ width: '100%', display: 'flex', flexDirection: popupInitial ? 'column' : chatScreen ? 'row' : 'column' }}>
      <div style={{ position: 'relative', flex: chatScreen ? '1' : undefined, width: '100%' }}>
        <Textarea
          ref={textareaRef}
          fontSize={fontSize}
          fontWeight={fontWeight}
          value={text}
          placeholder={placeholder}
          onChange={handleChange}
          maxRows={maxRows}
          maxLength={maxLength}
          onKeyDown={handleKeyDown}
          sx={{
            maxHeight: `${maxRows * 24}px`,
            overflowY: 'auto',
            '::placeholder': {
              color: colors.text.lastSync,
            },
            '&:disabled': {
              opacity: 0.5,
            },
          }}
          disabled={disabled}
        />
        <Typography
          sx={{
            fontSize: '11px',
            color: text.length >= maxLength * 0.9 ? colors.text.warning : colors.text.secondary,
            textAlign: 'right',
            mt: 0.25,
          }}
          data-testid='ask-nudgebee-prompt-char-counter'
        >
          {text.length.toLocaleString()} / {maxLength.toLocaleString()}
        </Typography>

        {showSuggestions && (
          <Popper
            open={showSuggestions}
            anchorEl={anchorEl}
            placement={suggestionsTrigger === 'button' ? 'top-start' : isFollowUp ? 'top-start' : 'bottom-start'}
            sx={{ zIndex: 9999 }}
            modifiers={[
              {
                name: 'offset',
                options: {
                  offset: [0, suggestionsTrigger === 'button' ? 8 : isFollowUp ? 8 : 80],
                },
              },
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
                  fallbackPlacements: ['bottom-start', 'top-start'],
                },
              },
            ]}
          >
            <ClickAwayListener
              onClickAway={() => {
                setShowSuggestions(false);
                setSelectedIndex(-1);
              }}
            >
              <Box
                sx={{
                  display: 'grid',
                  gridTemplateColumns:
                    (suggestionsTrigger === 'call' ? filteredFunctions.length : filteredSuggestions.length) <= 3 ? '1fr' : 'repeat(3, 1fr)',
                  gap: '2px',
                  padding: '8px',
                  border: '1px solid #BFDBFE',
                  borderRadius: '4px',
                  backgroundColor: '#fff',
                  width: (suggestionsTrigger === 'call' ? filteredFunctions.length : filteredSuggestions.length) <= 3 ? '200px' : '560px',
                  maxHeight: '238px',
                  overflowY: 'auto',
                  '&::-webkit-scrollbar': {
                    width: '4px',
                    borderRadius: '8px',
                  },
                  '@media (max-width: 1100px)': {
                    width: (suggestionsTrigger === 'call' ? filteredFunctions.length : filteredSuggestions.length) <= 3 ? '180px' : '490px',
                  },
                }}
              >
                {suggestionsTrigger === 'call'
                  ? filteredFunctions.map((func, index) => (
                      <Box
                        key={func.name}
                        sx={{
                          display: 'flex',
                          flexDirection: 'column',
                          alignItems: 'flex-start',
                          gap: '4px',
                          padding: '8px',
                          cursor: 'pointer',
                          textAlign: 'left',
                          backgroundColor: selectedIndex === index ? '#f0f0f0' : 'transparent',
                          '&:hover': { backgroundColor: '#EFF6FF', borderRadius: '4px', color: colors.text.primary },
                          fontSize: '12px',
                          fontWeight: 400,
                          color: colors.text.secondary,
                          '@media (max-width: 1300px)': {
                            fontSize: '11px',
                          },
                        }}
                        onClick={() => handleSelectSuggestion(func.name)}
                      >
                        <Typography sx={{ fontWeight: 600, color: colors.text.primary, fontSize: '12px' }}>{func.name}</Typography>
                        <Typography sx={{ fontSize: '10px', color: colors.text.tertiary, lineHeight: 1.2 }}>{func.description}</Typography>
                        {func.variables && func.variables.length > 0 && (
                          <Typography sx={{ fontSize: '9px', color: colors.text.secondary, fontStyle: 'italic' }}>
                            {func.variables.length} parameter{func.variables.length !== 1 ? 's' : ''}
                          </Typography>
                        )}
                      </Box>
                    ))
                  : filteredSuggestions.map((suggest, index) => (
                      <Box
                        key={suggest.name}
                        sx={{
                          display: 'flex',
                          alignItems: 'center',
                          gap: '10px',
                          padding: '8px',
                          cursor: 'pointer',
                          textAlign: 'left',
                          backgroundColor: selectedIndex === index ? '#f0f0f0' : 'transparent',
                          '&:hover': { backgroundColor: '#EFF6FF', borderRadius: '4px', color: colors.text.primary },
                          fontSize: '12px',
                          fontWeight: 400,
                          color: colors.text.secondary,
                          '@media (max-width: 1300px)': {
                            fontSize: '11px',
                            '& img': {
                              width: '14px',
                              height: '14px',
                            },
                          },
                        }}
                        onClick={() => handleSelectSuggestion(suggest.name)}
                      >
                        {getIcon(suggest.name) ? (
                          <SafeIcon src={getIcon(suggest.name)?.default || CustomAgentBlueIcon} alt='agent icon' width={20} height={20} />
                        ) : (
                          <Avatar
                            style={{
                              width: '12.6px',
                              height: '12.6px',
                              border: `1px solid ${colors.text.primaryLight}`,
                              color: `${colors.text.primaryLight}`,
                              backgroundColor: colors.white,
                              fontSize: '12px',
                              fontWeight: '500',
                              borderRadius: '4px',
                              padding: 0,
                            }}
                          >
                            {suggest.name[0].toUpperCase()}
                          </Avatar>
                        )}
                        {suggest.display_name}
                      </Box>
                    ))}
              </Box>
            </ClickAwayListener>
          </Popper>
        )}
      </div>
      {chatScreen && (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: '0px' }}>
          {/* Model Selector for chat screen */}
          {models && models.length > 0 && (
            <>
              <Box sx={{ width: '1px', height: '24px', backgroundColor: '#D0D0D0', mx: '12px' }} />
              <Box
                ref={modelButtonRef}
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  color: colors.text.tertiary,
                  border: `0.5px solid ${colors.text.tertiarymedium}`,
                  borderRadius: '4px',
                  padding: '4px 8px',
                  gap: '6px',
                  cursor: 'pointer',
                  fontSize: '12px',
                }}
                onClick={() => {
                  setShowModelDropdown(!showModelDropdown);
                  setModelAnchorEl(modelButtonRef.current);
                }}
              >
                <Typography sx={{ fontSize: '11px', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', maxWidth: '80px' }}>
                  {selectedModel ? selectedModel.model : 'Select Model'}
                </Typography>
                {selectedModel ? (
                  <Box
                    component='span'
                    sx={{
                      color: colors.text.secondary,
                      fontSize: '14px',
                      fontWeight: 'bold',
                      padding: '2px 6px',
                      borderRadius: '4px',
                      '&:hover': {
                        color: colors.text.primary,
                        backgroundColor: '#f0f0f0',
                      },
                    }}
                    onClick={(e) => {
                      e.stopPropagation();
                      onModelSelect?.(null);
                    }}
                  >
                    ✕
                  </Box>
                ) : (
                  <ArrowDropDownIcon sx={{ fontSize: '16px' }} />
                )}
              </Box>
            </>
          )}
          {showModelDropdown && chatScreen && (
            <Popper open={showModelDropdown} anchorEl={modelAnchorEl} placement='top-start' sx={{ zIndex: 9999 }}>
              <ClickAwayListener onClickAway={() => setShowModelDropdown(false)}>
                <Box
                  sx={{
                    display: 'flex',
                    flexDirection: 'column',
                    gap: '4px',
                    padding: '8px',
                    border: '1px solid #BFDBFE',
                    borderRadius: '4px',
                    backgroundColor: '#fff',
                    maxHeight: '200px',
                    overflowY: 'auto',
                    minWidth: '150px',
                  }}
                >
                  {models.map((model, index) => (
                    <Box
                      key={`chat-${model.provider}-${model.model}-${index}`}
                      sx={{
                        display: 'flex',
                        flexDirection: 'column',
                        padding: '6px',
                        cursor: 'pointer',
                        backgroundColor:
                          selectedModel?.provider === model.provider && selectedModel?.model === model.model ? '#EFF6FF' : 'transparent',
                        '&:hover': { backgroundColor: '#EFF6FF', borderRadius: '4px' },
                      }}
                      onClick={() => {
                        onModelSelect?.(model);
                        setShowModelDropdown(false);
                      }}
                    >
                      <Typography sx={{ fontSize: '11px', fontWeight: 500, color: colors.text.primary }}>{model.model}</Typography>
                      <Typography sx={{ fontSize: '9px', color: colors.text.tertiary }}>{model.provider}</Typography>
                    </Box>
                  ))}
                </Box>
              </ClickAwayListener>
            </Popper>
          )}
          <Box sx={{ width: '1px', height: '24px', backgroundColor: '#D0D0D0', mx: '12px' }} />
          <CustomButton
            size='Medium'
            onClick={() => {
              if (isFollowUp && allowStop) {
                buttonProperties.onClickStop();
              } else {
                const config = selectedModel ? { llm_provider: selectedModel.provider, llm_model_name: selectedModel.model } : undefined;
                buttonProperties.onClick(text, config);
                setText('');
              }
            }}
            startIcon={isFollowUp && allowStop ? <StopIcon sx={{ color: 'white' }} /> : ArrowRightWhiteIcon}
            disabled={!(isFollowUp && allowStop) && (!text || !buttonProperties.enable)}
          />
        </Box>
      )}

      {buttonProperties.show && !chatScreen ? (
        <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px', mt: '6px', pt: '6px', borderTop: `1px solid ${grey[200]}` }}>
          {/* Agent Selector */}
          <Box
            ref={agentButtonRef}
            sx={{
              display: 'flex',
              alignItems: 'center',
              color: colors.text.tertiary,
              border: `0.5px solid ${colors.text.tertiarymedium}`,
              borderRadius: '4px',
              padding: '4px 8px',
              gap: '4px',
              cursor: 'pointer',
              whiteSpace: 'nowrap',
              flexShrink: 0,
            }}
            onClick={handleButtonClick}
          >
            {selectedAgent ? (
              <>
                {getIcon(selectedAgent) ? (
                  <SafeIcon src={getIcon(selectedAgent)?.default} alt='agent icon' width={14} height={14} />
                ) : (
                  <Avatar
                    style={{
                      width: '16px',
                      height: '16px',
                      border: `1px solid ${colors.text.primaryLight}`,
                      color: `${colors.text.primaryLight}`,
                      backgroundColor: colors.white,
                      fontSize: '10px',
                      fontWeight: '500',
                      borderRadius: '3px',
                    }}
                  >
                    {selectedAgent[0].toUpperCase()}
                  </Avatar>
                )}
                <Typography sx={{ fontSize: '11px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: '60px' }}>
                  {selectedAgent}
                </Typography>
                <Box
                  component='span'
                  sx={{
                    color: colors.text.secondary,
                    fontSize: '12px',
                    fontWeight: 'bold',
                    flexShrink: 0,
                    lineHeight: 1,
                    '&:hover': { color: colors.text.primary },
                  }}
                  onClick={(e) => {
                    e.stopPropagation();
                    clearSelectedAgent();
                  }}
                >
                  ✕
                </Box>
              </>
            ) : (
              <>
                <Typography sx={{ fontSize: '11px' }}>Agent</Typography>
                <ArrowDropDownIcon sx={{ fontSize: '16px' }} />
              </>
            )}
          </Box>
          <Box sx={{ width: '1px', height: '18px', backgroundColor: grey[200], flexShrink: 0 }} />
          {/* Model Selector */}
          {models && models.length > 0 && (
            <Box
              ref={modelButtonRef}
              sx={{
                display: 'flex',
                alignItems: 'center',
                color: colors.text.tertiary,
                border: `0.5px solid ${colors.text.tertiarymedium}`,
                borderRadius: '4px',
                padding: '4px 8px',
                gap: '4px',
                cursor: 'pointer',
                whiteSpace: 'nowrap',
                flexShrink: 0,
              }}
              onClick={() => {
                setShowModelDropdown(!showModelDropdown);
                setModelAnchorEl(modelButtonRef.current);
              }}
            >
              {selectedModel ? (
                <>
                  <Typography sx={{ fontSize: '11px', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: '80px' }}>
                    {selectedModel.model}
                  </Typography>
                  <Box
                    component='span'
                    sx={{
                      color: colors.text.secondary,
                      fontSize: '12px',
                      fontWeight: 'bold',
                      flexShrink: 0,
                      lineHeight: 1,
                      '&:hover': { color: colors.text.primary },
                    }}
                    onClick={(e) => {
                      e.stopPropagation();
                      onModelSelect?.(null);
                    }}
                  >
                    ✕
                  </Box>
                </>
              ) : (
                <>
                  <Typography sx={{ fontSize: '11px' }}>Model</Typography>
                  <ArrowDropDownIcon sx={{ fontSize: '16px' }} />
                </>
              )}
            </Box>
          )}
          {showModelDropdown && !chatScreen && (
            <Popper open={showModelDropdown} anchorEl={modelAnchorEl} placement='top-start' sx={{ zIndex: 9999 }}>
              <ClickAwayListener onClickAway={() => setShowModelDropdown(false)}>
                <Box
                  sx={{
                    display: 'flex',
                    flexDirection: 'column',
                    gap: '4px',
                    padding: '8px',
                    border: '1px solid #BFDBFE',
                    borderRadius: '4px',
                    backgroundColor: '#fff',
                    maxHeight: '200px',
                    overflowY: 'auto',
                    minWidth: '180px',
                  }}
                >
                  {models.map((model, index) => (
                    <Box
                      key={`${model.provider}-${model.model}-${index}`}
                      sx={{
                        display: 'flex',
                        flexDirection: 'column',
                        padding: '8px',
                        cursor: 'pointer',
                        backgroundColor:
                          selectedModel?.provider === model.provider && selectedModel?.model === model.model ? '#EFF6FF' : 'transparent',
                        '&:hover': { backgroundColor: '#EFF6FF', borderRadius: '4px' },
                      }}
                      onClick={() => {
                        onModelSelect?.(model);
                        setShowModelDropdown(false);
                      }}
                    >
                      <Typography sx={{ fontSize: '12px', fontWeight: 500, color: colors.text.primary }}>{model.model}</Typography>
                      <Typography sx={{ fontSize: '10px', color: colors.text.tertiary }}>{model.provider}</Typography>
                    </Box>
                  ))}
                </Box>
              </ClickAwayListener>
            </Popper>
          )}
          <Box sx={{ flex: 1 }} />
          {/* Submit / Stop button */}
          <CustomButton
            id='set-config-btn'
            size='Medium'
            onClick={() => {
              if (isFollowUp && allowStop) {
                buttonProperties.onClickStop();
              } else {
                const config = selectedModel ? { llm_provider: selectedModel.provider, llm_model_name: selectedModel.model } : undefined;
                buttonProperties.onClick(text, config);
                setText('');
              }
            }}
            startIcon={isFollowUp && allowStop ? <StopIcon sx={{ color: 'white' }} /> : ArrowRightWhiteIcon}
            disabled={!(isFollowUp && allowStop) && (!text || !buttonProperties.enable)}
          />
        </Box>
      ) : null}
    </Box>
  );
};

export default AutoSuggestTextarea;
