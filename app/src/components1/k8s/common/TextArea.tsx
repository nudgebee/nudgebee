import CustomButton from '@components1/common/NewCustomButton';
import TextareaAutosize, { type TextareaAutosizeProps } from '@mui/material/TextareaAutosize';
import { Avatar, Box, ClickAwayListener, Popper, styled, Typography } from '@mui/material';
import type { Theme } from '@mui/material/styles';
import SafeIcon from '@components1/common/SafeIcon';
import React, { useEffect, useRef, useState } from 'react';
import { ArrowRightWhiteIcon, CustomAgentBlueIcon } from '@assets';
import { colors } from 'src/utils/colors';
import { getIcon } from '@components1/llm/common/AgentIcon';
import StopIcon from '@mui/icons-material/Stop';
import ArrowDropDownIcon from '@mui/icons-material/ArrowDropDown';

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
                  gap: '8px',
                  padding: '8px',
                  border: '1px solid #BFDBFE',
                  borderRadius: '4px',
                  backgroundColor: '#fff',
                  width: filteredSuggestions.length <= 3 ? '200px' : '560px',
                  maxHeight: '238px',
                  overflowY: 'auto',
                  '&::-webkit-scrollbar': {
                    width: '4px',
                    borderRadius: '8px',
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
                      gap: '10px',
                      padding: '8px',
                      cursor: 'pointer',
                      textAlign: 'left',
                      backgroundColor: selectedIndex === index ? '#f0f0f0' : 'transparent',
                      '&:hover': { backgroundColor: '#EFF6FF', borderRadius: '4px', color: colors.text.primary },
                      fontSize: '13px',
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
                          width: '20px',
                          height: '20px',
                          border: `1px solid ${colors.text.primaryLight}`,
                          color: `${colors.text.primaryLight}`,
                          backgroundColor: colors.white,
                          fontSize: '12px',
                          fontWeight: '500',
                          borderRadius: '4px',
                          padding: '1px 0px 0px',
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
        <Box sx={{ borderLeft: '0.75px solid #D0D0D0', pl: '17px' }}>
          <CustomButton
            sx={{ marginTop: '2px' }}
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
              gap: '12px',
              color: colors.text.secondaryDark,
              '& p': {
                fontSize: '12px',
              },
            }}
          >
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                color: colors.text.tertiary,
                border: `0.5px solid ${colors.text.tertiarymedium}`,
                borderRadius: '4px',
                padding: '4px 12px',
                width: 'fit-content',
                gap: '10px',
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
                        border: `1px solid ${colors.text.primaryLight}`,
                        color: `${colors.text.primaryLight}`,
                        backgroundColor: colors.white,
                        fontSize: '12px',
                        fontWeight: '500',
                        borderRadius: '4px',
                        padding: '1px 0px 0px',
                      }}
                    >
                      {selectedAgent[0].toUpperCase()}
                    </Avatar>
                  )}
                  <Typography>{selectedAgent}</Typography>
                  <Box
                    component='span'
                    sx={{
                      marginLeft: '8px',
                      color: colors.text.secondary,
                      '&:hover': {
                        color: colors.text.primary,
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
          <Box sx={{ display: 'flex', justifyContent: 'flex-end', gap: '12px' }}>
            <CustomButton
              id='set-text-btn'
              sx={{ marginTop: '2px' }}
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
