import { ArrowDown } from '@assets';
import Text from '@common-new/format/Text';
import { Box } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import { useState } from 'react';
import { Button } from '@components1/ds/Button';
import { ds } from '@utils/colors';
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
        p: `${ds.space.mul(0, 7)} ${ds.space[4]}`,
        border: '0.6px solid var(--ds-gray-300)',
        borderRadius: ds.radius.lg,
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
        color: 'var(--ds-gray-600)',
        '&:hover': {
          border: `0.5px solid ${'var(--ds-blue-400)'}`,
          color: 'var(--ds-brand-500)',
        },
      }}
    >
      {isHovered ? (
        <Text value={text} showFullText sx={{ fontSize: 'var(--ds-text-small)', fontStyle: 'italic' }} />
      ) : (
        <Text
          value={text.length > charLimit ? text.slice(0, charLimit) + '...' : text}
          sx={{ fontSize: 'var(--ds-text-small)', fontStyle: 'italic' }}
        />
      )}
      {icon && <Box sx={{ position: 'absolute', bottom: ds.space.mul(0, 5), '& img,svg': { height: ds.space[4], width: ds.space[4] } }}> {icon}</Box>}
      {text.length > charLimit && (
        <Box sx={{ height: '100%', display: 'flex', alignItems: 'center', width: ds.space.mul(1, 5), ml: ds.space[2] }}>
          <Box sx={{ position: 'absolute', right: ds.space[3] }}>
            {' '}
            <SafeIcon className={'ArrowDown'} src={ArrowDown} width={12} alt={'arrow-down'} />
          </Box>
          <Box sx={{ position: 'absolute', right: ds.space[3] }}>
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
                sx={{ minHeight: ds.space.mul(1, 20) }}
              />
            ))}
          </CustomSwiperCarousel>
        </Box>
      ) : (
        <Box sx={{ display: !showSlider ? 'grid' : '', gap: ds.space[3], gridTemplateColumns: 'repeat(3,1fr)', mb: ds.space.mul(0, 9) }}>
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
        <Button tone='secondary' size='xs' onClick={() => setShowMoreOpen(!showMoreOpen)}>
          {showMoreOpen ? 'Show Less' : 'Show More'}
        </Button>
      )}
    </>
  );
};

export default TextSelectionHoverBox;
