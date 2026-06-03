import CustomButton from '@components1/common/NewCustomButton';
import TextareaAutosize, { type TextareaAutosizeProps } from '@mui/material/TextareaAutosize';
import { Avatar, Box, ClickAwayListener, Popper, styled, Typography } from '@mui/material';
import type { Theme } from '@mui/material/styles';
import React, { useEffect, useRef, useState } from 'react';
import { ArrowRightWhiteIcon, CustomAgentBlueIcon } from '@assets';
import { ds } from 'src/utils/colors';
import { getIcon } from '@components1/llm/common/AgentIcon';
import StopIcon from '@mui/icons-material/Stop';
import ArrowDropDownIcon from '@mui/icons-material/ArrowDropDown';
import AttachFileIcon from '@mui/icons-material/AttachFile';
import CloseIcon from '@mui/icons-material/Close';
import SafeIcon from '@components1/common/SafeIcon';
import { toast as snackbar } from '@components1/ds/Toast';

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

// Wire format expected by the ai_execute_investigation `images` field.
export interface OutgoingImage {
  data: string; // base64, data-URI prefix stripped
  mime_type: string;
}

// Server-advertised image capability (from ai_list_models.image_support).
interface ImageSupport {
  enabled: boolean;
  maxPerMessage: number;
  maxSizeMb: number;
  allowedMimeTypes: string[];
}

interface AttachedImage extends OutgoingImage {
  id: string;
  name: string;
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
    onClick: (e: string, config?: { llm_provider?: string; llm_model_name?: string }, images?: OutgoingImage[]) => void;
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
  imageSupport?: ImageSupport;
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
  imageSupport,
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
  const [attachedImages, setAttachedImages] = useState<AttachedImage[]>([]);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  const imagesEnabled = !!imageSupport?.enabled;
  const allowedMimeTypes = imageSupport?.allowedMimeTypes ?? [];
  const maxPerMessage = imageSupport?.maxPerMessage ?? 0;
  const maxSizeMb = imageSupport?.maxSizeMb ?? 0;

  // Validate + read selected/pasted files into base64 attachments. Limits
  // mirror the server's advertised image_support so we fail fast with a clear
  // message instead of letting the request 400 server-side.
  //
  // `scheduled` counts files synchronously queued for read so a single
  // multi-file paste/drop can't bypass `maxPerMessage`; the async setter also
  // re-checks against `cur` to close the race when a second batch fires
  // before the first batch's readers resolve.
  const addFiles = (files: File[]) => {
    if (!imagesEnabled || files.length === 0) return;
    let scheduled = attachedImages.length;
    for (const file of files) {
      if (maxPerMessage > 0 && scheduled >= maxPerMessage) {
        snackbar.error(`You can attach at most ${maxPerMessage} image${maxPerMessage === 1 ? '' : 's'} per message.`);
        break;
      }
      if (allowedMimeTypes.length > 0 && !allowedMimeTypes.includes(file.type)) {
        snackbar.error(`Unsupported image type "${file.type || 'unknown'}". Allowed: ${allowedMimeTypes.join(', ')}.`);
        continue;
      }
      if (maxSizeMb > 0 && file.size > maxSizeMb * 1024 * 1024) {
        snackbar.error(`"${file.name || 'image'}" exceeds the ${maxSizeMb} MB limit.`);
        continue;
      }
      scheduled++;
      const reader = new FileReader();
      const id = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
      reader.onload = () => {
        const result = typeof reader.result === 'string' ? reader.result : '';
        // strip "data:<mime>;base64," prefix — backend wants raw base64
        const base64 = result.includes(',') ? result.slice(result.indexOf(',') + 1) : result;
        if (!base64) return;
        setAttachedImages((cur) => {
          if (maxPerMessage > 0 && cur.length >= maxPerMessage) return cur;
          if (cur.some((img) => img.id === id)) return cur;
          return [...cur, { id, name: file.name || 'image', data: base64, mime_type: file.type }];
        });
      };
      reader.onerror = () => snackbar.error(`Failed to read "${file.name || 'image'}".`);
      reader.readAsDataURL(file);
    }
  };

  const removeImage = (id: string) => setAttachedImages((prev) => prev.filter((img) => img.id !== id));

  const handlePaste = (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    if (!imagesEnabled) return;
    const imageFiles = Array.from(e.clipboardData?.files ?? []).filter((f) => f.type.startsWith('image/'));
    if (imageFiles.length > 0) {
      e.preventDefault();
      addFiles(imageFiles);
    }
  };

  // Single send path used by every submit affordance so text + image clearing
  // and the outgoing payload stay consistent.
  const handleSend = () => {
    const config = selectedModel ? { llm_provider: selectedModel.provider, llm_model_name: selectedModel.model } : undefined;
    const images: OutgoingImage[] = attachedImages.map(({ data, mime_type }) => ({ data, mime_type }));
    buttonProperties.onClick(text, config, images.length ? images : undefined);
    setText('');
    setAttachedImages([]);
  };

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
          onPaste={handlePaste}
          sx={{
            maxHeight: `${maxRows * 24}px`,
            overflowY: 'auto',
            '::placeholder': {
              color: ds.gray[400],
            },
            '&:disabled': {
              opacity: 0.5,
            },
          }}
          disabled={disabled}
        />
        <Typography
          sx={{
            fontSize: 'var(--ds-text-caption)',
            color: text.length >= maxLength * 0.9 ? ds.amber[700] : ds.gray[700],
            textAlign: 'right',
            mt: ds.space[0],
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
                  gap: 'var(--ds-space-1)',
                  padding: 'var(--ds-space-2)',
                  border: '1px solid var(--ds-blue-300)',
                  borderRadius: 'var(--ds-radius-sm)',
                  backgroundColor: 'var(--ds-background-100)',
                  width:
                    (suggestionsTrigger === 'call' ? filteredFunctions.length : filteredSuggestions.length) <= 3
                      ? ds.space.mul(0, 100)
                      : ds.space.mul(0, 280),
                  maxHeight: ds.space.mul(0, 119),
                  overflowY: 'auto',
                  '&::-webkit-scrollbar': {
                    width: ds.space[1],
                    borderRadius: 'var(--ds-radius-lg)',
                  },
                  '@media (max-width: 1100px)': {
                    width:
                      (suggestionsTrigger === 'call' ? filteredFunctions.length : filteredSuggestions.length) <= 3
                        ? ds.space.mul(0, 90)
                        : ds.space.mul(0, 245),
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
                          gap: 'var(--ds-space-1)',
                          padding: 'var(--ds-space-2)',
                          cursor: 'pointer',
                          textAlign: 'left',
                          backgroundColor: selectedIndex === index ? ds.gray[100] : 'transparent',
                          '&:hover': { backgroundColor: 'var(--ds-blue-100)', borderRadius: 'var(--ds-radius-sm)', color: ds.blue[500] },
                          fontSize: 'var(--ds-text-small)',
                          fontWeight: 'var(--ds-font-weight-regular)',
                          color: ds.gray[700],
                          '@media (max-width: 1300px)': {
                            fontSize: 'var(--ds-text-caption)',
                          },
                        }}
                        onClick={() => handleSelectSuggestion(func.name)}
                      >
                        <Typography sx={{ fontWeight: 'var(--ds-font-weight-semibold)', color: ds.blue[500], fontSize: 'var(--ds-text-small)' }}>
                          {func.name}
                        </Typography>
                        <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600], lineHeight: 1.2 }}>{func.description}</Typography>
                        {func.variables && func.variables.length > 0 && (
                          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[700], fontStyle: 'italic' }}>
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
                          gap: 'var(--ds-space-2)',
                          padding: 'var(--ds-space-2)',
                          cursor: 'pointer',
                          textAlign: 'left',
                          backgroundColor: selectedIndex === index ? ds.gray[100] : 'transparent',
                          '&:hover': { backgroundColor: 'var(--ds-blue-100)', borderRadius: 'var(--ds-radius-sm)', color: ds.blue[500] },
                          fontSize: 'var(--ds-text-small)',
                          fontWeight: 'var(--ds-font-weight-regular)',
                          color: ds.gray[700],
                          '@media (max-width: 1300px)': {
                            fontSize: 'var(--ds-text-caption)',
                            '& img': {
                              width: ds.space.mul(0, 7),
                              height: ds.space.mul(0, 7),
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
                              width: ds.space[3],
                              height: ds.space[3],
                              border: `1px solid ${ds.blue[400]}`,
                              color: `${ds.blue[400]}`,
                              backgroundColor: ds.background[100],
                              fontSize: 'var(--ds-text-small)',
                              fontWeight: 'var(--ds-font-weight-medium)',
                              borderRadius: 'var(--ds-radius-sm)',
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
              <Box sx={{ width: '1px', height: ds.space[5], backgroundColor: 'var(--ds-brand-200)', mx: 'var(--ds-space-3)' }} />
              <Box
                ref={modelButtonRef}
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  color: ds.gray[600],
                  border: `0.5px solid ${ds.gray[300]}`,
                  borderRadius: 'var(--ds-radius-sm)',
                  padding: 'var(--ds-space-1) var(--ds-space-2)',
                  gap: 'var(--ds-space-1)',
                  cursor: 'pointer',
                  fontSize: 'var(--ds-text-small)',
                }}
                onClick={() => {
                  setShowModelDropdown(!showModelDropdown);
                  setModelAnchorEl(modelButtonRef.current);
                }}
              >
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-caption)',
                    whiteSpace: 'nowrap',
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    maxWidth: ds.space.mul(0, 40),
                  }}
                >
                  {selectedModel ? selectedModel.model : 'Select Model'}
                </Typography>
                {selectedModel ? (
                  <Box
                    component='span'
                    sx={{
                      color: ds.gray[700],
                      fontSize: 'var(--ds-text-body-lg)',
                      fontWeight: 'bold',
                      padding: 'var(--ds-space-1) var(--ds-space-1)',
                      borderRadius: 'var(--ds-radius-sm)',
                      '&:hover': {
                        color: ds.blue[500],
                        backgroundColor: 'var(--ds-gray-100)',
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
                  <ArrowDropDownIcon sx={{ fontSize: 'var(--ds-text-title)' }} />
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
                    gap: 'var(--ds-space-1)',
                    padding: 'var(--ds-space-2)',
                    border: '1px solid var(--ds-blue-300)',
                    borderRadius: 'var(--ds-radius-sm)',
                    backgroundColor: 'var(--ds-background-100)',
                    maxHeight: ds.space.mul(0, 100),
                    overflowY: 'auto',
                    minWidth: ds.space.mul(0, 75),
                  }}
                >
                  {models.map((model, index) => (
                    <Box
                      key={`chat-${model.provider}-${model.model}-${index}`}
                      sx={{
                        display: 'flex',
                        flexDirection: 'column',
                        padding: 'var(--ds-space-1)',
                        cursor: 'pointer',
                        backgroundColor:
                          selectedModel?.provider === model.provider && selectedModel?.model === model.model ? ds.blue[100] : 'transparent',
                        '&:hover': { backgroundColor: 'var(--ds-blue-100)', borderRadius: 'var(--ds-radius-sm)' },
                      }}
                      onClick={() => {
                        onModelSelect?.(model);
                        setShowModelDropdown(false);
                      }}
                    >
                      <Typography sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.blue[500] }}>
                        {model.model}
                      </Typography>
                      <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600] }}>{model.provider}</Typography>
                    </Box>
                  ))}
                </Box>
              </ClickAwayListener>
            </Popper>
          )}
          <Box sx={{ width: '1px', height: ds.space[5], backgroundColor: 'var(--ds-brand-200)', mx: 'var(--ds-space-3)' }} />
          <CustomButton
            size='Medium'
            onClick={() => {
              if (isFollowUp && allowStop) {
                buttonProperties.onClickStop();
              } else {
                handleSend();
              }
            }}
            startIcon={isFollowUp && allowStop ? <StopIcon sx={{ color: 'white' }} /> : ArrowRightWhiteIcon}
            disabled={!(isFollowUp && allowStop) && (!text || !buttonProperties.enable)}
          />
        </Box>
      )}

      {imagesEnabled && attachedImages.length > 0 && (
        <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--ds-space-2)', mt: 'var(--ds-space-2)' }}>
          {attachedImages.map((img) => (
            <Box
              key={img.id}
              title={img.name}
              sx={{
                position: 'relative',
                width: ds.space.mul(0, 28),
                height: ds.space.mul(0, 28),
                borderRadius: 'var(--ds-radius-md)',
                overflow: 'hidden',
                border: `1px solid ${grey[200]}`,
                backgroundColor: grey[50],
              }}
            >
              <Box
                component='img'
                src={`data:${img.mime_type};base64,${img.data}`}
                alt={img.name}
                sx={{ width: '100%', height: '100%', objectFit: 'cover', display: 'block' }}
              />
              <Box
                role='button'
                aria-label={`Remove ${img.name}`}
                data-testid={`remove-attached-image-${img.id}`}
                onClick={() => removeImage(img.id)}
                sx={{
                  position: 'absolute',
                  top: ds.space[0],
                  right: ds.space[0],
                  width: ds.space[4],
                  height: ds.space[4],
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  borderRadius: '50%',
                  backgroundColor: 'rgba(0,0,0,0.55)',
                  cursor: 'pointer',
                  '&:hover': { backgroundColor: 'rgba(0,0,0,0.8)' },
                }}
              >
                <CloseIcon sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-background-100)' }} />
              </Box>
            </Box>
          ))}
        </Box>
      )}

      {buttonProperties.show && !chatScreen ? (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: 'var(--ds-space-2)',
            mt: 'var(--ds-space-1)',
            pt: 'var(--ds-space-1)',
            borderTop: `1px solid ${grey[200]}`,
          }}
        >
          {imagesEnabled && (
            <>
              <input
                ref={fileInputRef}
                type='file'
                accept={allowedMimeTypes.length > 0 ? allowedMimeTypes.join(',') : 'image/*'}
                multiple
                style={{ display: 'none' }}
                onChange={(e) => {
                  addFiles(Array.from(e.target.files ?? []));
                  e.target.value = '';
                }}
                data-testid='chat-image-file-input'
              />
              <Box
                role='button'
                aria-label='Attach image'
                data-testid='chat-attach-image-btn'
                onClick={() => fileInputRef.current?.click()}
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  cursor: 'pointer',
                  p: 'var(--ds-space-1)',
                  borderRadius: 'var(--ds-radius-sm)',
                  '&:hover': { backgroundColor: grey[50] },
                }}
              >
                <AttachFileIcon sx={{ fontSize: 'var(--ds-text-title)', color: ds.gray[600] }} />
              </Box>
            </>
          )}
          {/* Agent Selector */}
          <Box
            ref={agentButtonRef}
            sx={{
              display: 'flex',
              alignItems: 'center',
              color: ds.gray[600],
              border: `0.5px solid ${ds.gray[300]}`,
              borderRadius: 'var(--ds-radius-sm)',
              padding: 'var(--ds-space-1) var(--ds-space-2)',
              gap: 'var(--ds-space-1)',
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
                      width: ds.space[4],
                      height: ds.space[4],
                      border: `1px solid ${ds.blue[400]}`,
                      color: `${ds.blue[400]}`,
                      backgroundColor: ds.background[100],
                      fontSize: 'var(--ds-text-caption)',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      borderRadius: 'var(--ds-radius-sm)',
                    }}
                  >
                    {selectedAgent[0].toUpperCase()}
                  </Avatar>
                )}
                <Typography
                  sx={{
                    fontSize: 'var(--ds-text-caption)',
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                    maxWidth: ds.space.mul(0, 30),
                  }}
                >
                  {selectedAgent}
                </Typography>
                <Box
                  component='span'
                  sx={{
                    color: ds.gray[700],
                    fontSize: 'var(--ds-text-small)',
                    fontWeight: 'bold',
                    flexShrink: 0,
                    lineHeight: 1,
                    '&:hover': { color: ds.blue[500] },
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
                <Typography sx={{ fontSize: 'var(--ds-text-caption)' }}>Agent</Typography>
                <ArrowDropDownIcon sx={{ fontSize: 'var(--ds-text-title)' }} />
              </>
            )}
          </Box>
          <Box sx={{ width: '1px', height: ds.space.mul(0, 9), backgroundColor: grey[200], flexShrink: 0 }} />
          {/* Model Selector */}
          {models && models.length > 0 && (
            <Box
              ref={modelButtonRef}
              sx={{
                display: 'flex',
                alignItems: 'center',
                color: ds.gray[600],
                border: `0.5px solid ${ds.gray[300]}`,
                borderRadius: 'var(--ds-radius-sm)',
                padding: 'var(--ds-space-1) var(--ds-space-2)',
                gap: 'var(--ds-space-1)',
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
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-caption)',
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap',
                      maxWidth: ds.space.mul(0, 40),
                    }}
                  >
                    {selectedModel.model}
                  </Typography>
                  <Box
                    component='span'
                    sx={{
                      color: ds.gray[700],
                      fontSize: 'var(--ds-text-small)',
                      fontWeight: 'bold',
                      flexShrink: 0,
                      lineHeight: 1,
                      '&:hover': { color: ds.blue[500] },
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
                  <Typography sx={{ fontSize: 'var(--ds-text-caption)' }}>Model</Typography>
                  <ArrowDropDownIcon sx={{ fontSize: 'var(--ds-text-title)' }} />
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
                    gap: 'var(--ds-space-1)',
                    padding: 'var(--ds-space-2)',
                    border: '1px solid var(--ds-blue-300)',
                    borderRadius: 'var(--ds-radius-sm)',
                    backgroundColor: 'var(--ds-background-100)',
                    maxHeight: ds.space.mul(0, 100),
                    overflowY: 'auto',
                    minWidth: ds.space.mul(0, 90),
                  }}
                >
                  {models.map((model, index) => (
                    <Box
                      key={`${model.provider}-${model.model}-${index}`}
                      sx={{
                        display: 'flex',
                        flexDirection: 'column',
                        padding: 'var(--ds-space-2)',
                        cursor: 'pointer',
                        backgroundColor:
                          selectedModel?.provider === model.provider && selectedModel?.model === model.model ? ds.blue[100] : 'transparent',
                        '&:hover': { backgroundColor: 'var(--ds-blue-100)', borderRadius: 'var(--ds-radius-sm)' },
                      }}
                      onClick={() => {
                        onModelSelect?.(model);
                        setShowModelDropdown(false);
                      }}
                    >
                      <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.blue[500] }}>
                        {model.model}
                      </Typography>
                      <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.gray[600] }}>{model.provider}</Typography>
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
                handleSend();
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
