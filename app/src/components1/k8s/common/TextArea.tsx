import CustomButton from '@components1/common/NewCustomButton';
import TextareaAutosize, { type TextareaAutosizeProps } from '@mui/material/TextareaAutosize';
import { Avatar, Box, ClickAwayListener, Popper, styled, Typography } from '@mui/material';
import type { Theme } from '@mui/material/styles';
import SafeIcon from '@components1/common/SafeIcon';
import React, { useEffect, useRef, useState } from 'react';
import { ArrowRightWhiteIcon, CustomAgentBlueIcon } from '@assets';
import { ds } from 'src/utils/colors';
import { getIcon } from '@components1/llm/common/AgentIcon';
import StopIcon from '@mui/icons-material/Stop';
import ArrowDropDownIcon from '@mui/icons-material/ArrowDropDown';

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
    padding: var(--ds-space-2) var(--ds-space-3);
    border-radius: var(--ds-radius-lg);
    color: ${ds.gray[700]};
    background: ${ds.background[100]};
    border: 1px solid ${ds.gray[300]};
    box-shadow: 0px 2px 2px ${ds.background[300]};
    max-height: calc(${maxRows} * 1.5em + 16px);
    overflow-y: auto !important;
    resize: vertical;
    &:hover {
      border-color: ${ds.blue[500]};
    }

    &:focus {
      border-color: ${ds.blue[500]};
      box-shadow: 0 0 0 3px ${ds.blue[300]};
    }

    // firefox
    &:focus-visible {
      outline: 0;
    }

    &::-webkit-scrollbar {
      width: calc(var(--ds-space-0) * 3);
      display: none;
    }

    &:hover::-webkit-scrollbar {
      display: block;
    }

    &::-webkit-scrollbar-track {
      border-radius: var(--ds-radius-sm);
      background-color: ${ds.gray[300]};
    }

    &::-webkit-scrollbar-thumb {
      background-color: ${ds.gray[400]};
      border-radius: var(--ds-radius-sm);
    }

    &::-webkit-scrollbar-thumb:hover {
      background-color: ${ds.gray[400]};
    }
  `
);

interface AutoSuggestTextareaProps {
  value: string;
  suggestionsAt: { name: string; display_name: string }[];
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
    onClick: (e: string) => void;
    onClickStop: () => void;
  };
  chatScreen?: boolean;
  isFollowUp?: boolean;
  disabled?: boolean;
  allowStop?: boolean;
}

const AutoSuggestTextarea: React.FC<AutoSuggestTextareaProps> = ({
  value,
  suggestionsAt,
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
}) => {
  const [text, setText] = useState('');
  const [showSuggestions, setShowSuggestions] = useState(false);
  const [anchorEl, setAnchorEl] = useState<null | HTMLElement>(null);
  const [filteredSuggestions, setFilteredSuggestions] = useState<{ name: string; display_name: string }[]>([]);
  const [selectedIndex, setSelectedIndex] = useState(-1);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const [suggestionsTrigger, setSuggestionsTrigger] = useState<'at' | 'button'>('at');
  const [selectedAgent, setSelectedAgent] = useState<string | null>(null);

  const handleChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const value = e.target.value.replace(/^\n+/, '');
    setText(value);
    const match = RegExp(/^@(\w+)/).exec(value);
    const typedSuggest = match ? match[1].trim().toLowerCase() : '';
    const matchedSuggestions = suggestionsAt.filter(
      (suggest) => suggest.name.toLowerCase().startsWith(typedSuggest) || suggest.display_name.toLowerCase().startsWith(typedSuggest)
    );
    if (value.startsWith('@') && suggestionsAt.length > 0 && matchedSuggestions.length > 0) {
      setSuggestionsTrigger('at');
      setFilteredSuggestions(matchedSuggestions);
      setShowSuggestions(true);
      setSelectedIndex(-1);
      const isSuggestionPresent = matchedSuggestions.some(
        (suggest) => suggest.name.toLowerCase() === typedSuggest || suggest.display_name.toLowerCase() === typedSuggest
      );
      if (isSuggestionPresent) {
        setShowSuggestions(false);
      }
      setAnchorEl(textareaRef.current);
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
    } else {
      setText((prev) => prev + '@' + suggest + ' ');
    }
    setSelectedAgent(suggest);
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
      switch (e.key) {
        case 'ArrowDown':
          e.preventDefault();
          setSelectedIndex((prev) => (prev < filteredSuggestions.length - 1 ? prev + 1 : 0));
          break;
        case 'ArrowUp':
          e.preventDefault();
          setSelectedIndex((prev) => (prev > 0 ? prev - 1 : filteredSuggestions.length - 1));
          break;
        case 'Enter':
          e.preventDefault();
          if (selectedIndex >= 0) {
            handleSelectSuggestion(filteredSuggestions[selectedIndex].name);
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
      setAnchorEl(textareaRef.current);
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
    <Box sx={{ width: '100%', display: chatScreen ? 'flex' : 'block' }}>
      <div style={{ position: 'relative', flex: '1' }}>
        <Textarea
          ref={textareaRef}
          fontSize={fontSize}
          fontWeight={fontWeight}
          value={text.trimStart()}
          placeholder={placeholder}
          onChange={handleChange}
          maxRows={maxRows}
          maxLength={maxLength}
          onKeyDown={handleKeyDown}
          sx={{
            maxHeight: `${maxRows * 24}px`,
            overflowY: 'auto',
            '&:disabled': {
              opacity: 0.5,
            },
          }}
          disabled={disabled}
        />

        {showSuggestions && (
          <Popper
            open={showSuggestions}
            anchorEl={anchorEl}
            placement={isFollowUp ? 'top-start' : 'bottom-start'}
            sx={{ transform: isFollowUp ? 'auto' : 'translate3d(592px, 443px, 0px) !important' }}
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
                  gridTemplateColumns: filteredSuggestions.length <= 3 ? '1fr' : 'repeat(3, 1fr)',
                  gap: 'var(--ds-space-2)',
                  padding: 'var(--ds-space-2)',
                  border: '1px solid var(--ds-blue-300)',
                  borderRadius: 'var(--ds-radius-sm)',
                  backgroundColor: 'var(--ds-background-100)',
                  width: filteredSuggestions.length <= 3 ? '200px' : '560px',
                  maxHeight: '238px',
                  overflowY: 'auto',
                  '&::-webkit-scrollbar': {
                    width: '4px',
                    borderRadius: 'var(--ds-radius-lg)',
                  },
                  '@media (max-width: 1100px)': {
                    width: filteredSuggestions.length <= 3 ? '180px' : '490px',
                  },
                }}
              >
                {filteredSuggestions.map((suggest, index) => (
                  <Box
                    key={suggest.name}
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: 'var(--ds-space-2)',
                      padding: 'var(--ds-space-2)',
                      cursor: 'pointer',
                      textAlign: 'left',
                      backgroundColor: selectedIndex === index ? '#f0f0f0' : 'transparent',
                      '&:hover': { backgroundColor: 'var(--ds-blue-100)', borderRadius: 'var(--ds-radius-sm)', color: ds.blue[500] },
                      fontSize: 'var(--ds-text-body)',
                      fontWeight: 'var(--ds-font-weight-regular)',
                      color: ds.gray[700],
                      '@media (max-width: 1300px)': {
                        fontSize: 'var(--ds-text-caption)',
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
                          width: '20px',
                          height: '20px',
                          border: `1px solid ${ds.blue[400]}`,
                          color: `${ds.blue[400]}`,
                          backgroundColor: ds.background[100],
                          fontSize: 'var(--ds-text-small)',
                          fontWeight: 'var(--ds-font-weight-medium)',
                          borderRadius: 'var(--ds-radius-sm)',
                          padding: 'var(--ds-space-1) 0px 0px',
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
        <Box sx={{ borderLeft: '0.75px solid var(--ds-brand-200)', pl: 'var(--ds-space-4)' }}>
          <CustomButton
            sx={{ marginTop: 'var(--ds-space-1)' }}
            size='Medium'
            onClick={() => {
              if (isFollowUp && allowStop) {
                buttonProperties.onClickStop();
              } else {
                buttonProperties.onClick(text);
              }
            }}
            startIcon={isFollowUp && allowStop ? <StopIcon sx={{ color: 'white' }} /> : ArrowRightWhiteIcon}
            disabled={!(isFollowUp && allowStop) && (!text || !buttonProperties.enable)}
          />
        </Box>
      )}

      {buttonProperties.show && !chatScreen ? (
        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: 'var(--ds-space-3)',
              color: ds.gray[400],
              '& p': {
                fontSize: 'var(--ds-text-small)',
              },
            }}
          >
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                color: ds.gray[600],
                border: `0.5px solid ${ds.gray[300]}`,
                borderRadius: 'var(--ds-radius-sm)',
                padding: 'var(--ds-space-1) var(--ds-space-3)',
                width: 'fit-content',
                gap: 'var(--ds-space-2)',
                cursor: 'pointer',
              }}
              onClick={handleButtonClick}
            >
              {selectedAgent ? (
                <>
                  {getIcon(selectedAgent) ? (
                    <SafeIcon src={getIcon(selectedAgent)?.default} alt='agent icon' width={16} height={16} />
                  ) : (
                    <Avatar
                      style={{
                        width: '20px',
                        height: '20px',
                        border: `1px solid ${ds.blue[400]}`,
                        color: `${ds.blue[400]}`,
                        backgroundColor: ds.background[100],
                        fontSize: 'var(--ds-text-small)',
                        fontWeight: 'var(--ds-font-weight-medium)',
                        borderRadius: 'var(--ds-radius-sm)',
                        padding: 'var(--ds-space-1) 0px 0px',
                      }}
                    >
                      {selectedAgent[0].toUpperCase()}
                    </Avatar>
                  )}
                  <Typography>{selectedAgent}</Typography>
                  <Box
                    component='span'
                    sx={{
                      marginLeft: 'var(--ds-space-2)',
                      color: ds.gray[700],
                      '&:hover': {
                        color: ds.blue[500],
                      },
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
                  <Typography>Select Agent</Typography>
                  <ArrowDropDownIcon />
                </>
              )}
            </Box>
            <Typography>or use @</Typography>
          </Box>
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--ds-space-3)' }}>
            <CustomButton
              id='set-text-btn'
              sx={{ marginTop: 'var(--ds-space-1)' }}
              size='Medium'
              onClick={() => {
                buttonProperties.onClick(text);
              }}
              startIcon={ArrowRightWhiteIcon}
              disabled={!text || !buttonProperties.enable}
            />
          </Box>
        </Box>
      ) : null}
    </Box>
  );
};

export default AutoSuggestTextarea;
