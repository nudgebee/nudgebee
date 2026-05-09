import { ArrowDown } from '@assets';
import { Text } from '@components1/common';
import { Box } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import { colors } from 'src/utils/colors';
import { useState } from 'react';
import CustomButton from '@components1/common/NewCustomButton';
import CustomSwiperCarousel from '@components1/common/CustomSwiperCarousel';
interface TextSelectionHoverBoxProps {
  handleTextSelection: (text: string) => void;
  textList: Array<{ text: string; icon?: JSX.Element }>;
  charLimit: number;
  className?: string;
  showSlider: boolean;
}

interface ExpandableTextboxProps {
  text: string;
  icon?: any;
  className?: string;
  charLimit: number;
  sx?: Record<string, any>;
  textSelection: (text: string) => void;
}

const ExpandableTextbox: React.FC<ExpandableTextboxProps> = ({ text, charLimit, textSelection, sx = {}, icon }) => {
  const [isHovered, setIsHovered] = useState(false);

  return (
    <Box
      onClick={() => {
        textSelection(text);
      }}
      onMouseEnter={() => setIsHovered(true)}
      onMouseLeave={() => setIsHovered(false)}
      sx={{
        ...sx,
        position: 'relative',
        cursor: 'pointer',
        p: '14px 16px',
        border: '0.6px solid #D0D0D0',
        borderRadius: '8px',
        display: 'flex',

        '& .ArrowDown': {
          opacity: 1,
          transition: 'opacity 0.3s ease-in-out',
        },
        '& .ArrowUp': {
          opacity: 0,
          transition: 'opacity 0.3s ease-in-out',
        },
        '&:hover .ArrowDown': {
          opacity: 0,
        },
        '&:hover .ArrowUp': {
          opacity: 1,
        },
        color: '#737373',
        '&:hover': {
          border: `0.5px solid ${colors.border.primaryLightest}`,
          color: '#374151',
        },
      }}
    >
      {isHovered ? (
        <Text value={text} showFullText sx={{ fontSize: '12px', fontStyle: 'italic' }} />
      ) : (
        <Text value={text.length > charLimit ? text.slice(0, charLimit) + '...' : text} sx={{ fontSize: '12px', fontStyle: 'italic' }} />
      )}
      {icon && <Box sx={{ position: 'absolute', bottom: '10px', '& img,svg': { height: '16px', width: '16px' } }}> {icon}</Box>}
      {text.length > charLimit && (
        <Box sx={{ height: '100%', display: 'flex', alignItems: 'center', width: '20px', ml: '8px' }}>
          <Box sx={{ position: 'absolute', right: '12px' }}>
            {' '}
            <SafeIcon className={'ArrowDown'} src={ArrowDown} width={12} alt={'arrow-down'} />
          </Box>
          <Box sx={{ position: 'absolute', right: '12px' }}>
            {' '}
            <SafeIcon style={{ transform: 'rotate(180deg)' }} className={'ArrowUp'} src={ArrowDown} width={12} alt={'arrow-up'} />
          </Box>
        </Box>
      )}
    </Box>
  );
};

const TextSelectionHoverBox: React.FC<TextSelectionHoverBoxProps> = ({
  textList = [],
  charLimit = 20,
  handleTextSelection,
  className = '',
  showSlider = false,
}) => {
  const [showMoreOpen, setShowMoreOpen] = useState(false);

  return (
    <>
      {showSlider ? (
        <Box sx={{ position: 'relative' }}>
          <CustomSwiperCarousel
            slidesToShow={3}
            showArrows
            arrowsPosition='outside'
            breakpoints={{
              1299: {
                slidesPerView: 2,
                slidesPerGroup: 2,
              },
              767: {
                slidesPerView: 1,
                slidesPerGroup: 1,
              },
            }}
          >
            {textList.map((item) => (
              <ExpandableTextbox
                className={className}
                key={item.text}
                text={item.text}
                icon={item.icon}
                charLimit={charLimit}
                textSelection={handleTextSelection}
                sx={{ minHeight: '80px' }}
              />
            ))}
          </CustomSwiperCarousel>
        </Box>
      ) : (
        <Box sx={{ display: !showSlider ? 'grid' : '', gap: '12px', gridTemplateColumns: 'repeat(3,1fr)', mb: '18px' }}>
          {!showMoreOpen
            ? textList.slice(0, 6).map((item) => {
                return (
                  <ExpandableTextbox
                    className={className}
                    key={item.text}
                    text={item.text}
                    icon={item.icon}
                    charLimit={charLimit}
                    textSelection={handleTextSelection}
                  />
                );
              })
            : textList.map((item) => {
                return (
                  <ExpandableTextbox
                    className={className}
                    key={item.text}
                    text={item.text}
                    icon={item.icon}
                    charLimit={charLimit}
                    textSelection={handleTextSelection}
                  />
                );
              })}
        </Box>
      )}

      {textList.length > 6 && (
        <CustomButton
          sx={{ fontWeight: '400 !important', fontSize: '12px !important', background: 'transparent !important' }}
          text={showMoreOpen ? 'Show Less' : 'Show More'}
          variant={'secondary'}
          size={'xSmall'}
          onClick={() => setShowMoreOpen(!showMoreOpen)}
        />
      )}
    </>
  );
};

export default TextSelectionHoverBox;
