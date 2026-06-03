import React, { useState } from 'react';
import { List, ListItem, Typography, Box } from '@mui/material';
import { Divider } from '@components1/ds/Divider';
import CustomTooltip from '@components1/common/CustomTooltip';

const MAX_ITEM_LENGTH = 120;
const TOOLTIP_MAX_HEIGHT = '400px';

interface ShowMoreListProps {
  data: string[];
  initialCount?: number;
  maxItemLength?: number;
  onItemClick?: (item: string) => void;
}

const ShowMoreList: React.FC<ShowMoreListProps> = ({ data, initialCount = 5, maxItemLength = MAX_ITEM_LENGTH, onItemClick }) => {
  const [showAll, setShowAll] = useState(false);

  const toggleShowAll = () => setShowAll((prev) => !prev);

  const displayedData = showAll ? data : data.slice(0, initialCount);

  return (
    <List sx={{ width: '100%' }}>
      {displayedData.map((text) => {
        const needsTruncation = text.length > maxItemLength;
        const displayText = needsTruncation ? text.slice(0, maxItemLength) + '…' : text;

        return (
          <ListItem key={text} alignItems='flex-start' sx={{ p: '0px var(--ds-space-4) var(--ds-space-1) var(--ds-space-1)' }}>
            <Box
              sx={{
                width: '5px',
                height: '5px',
                bgcolor: 'var(--ds-brand-500)',
                borderRadius: '100%',
                marginTop: 'var(--ds-space-2)',
                marginRight: 'var(--ds-space-1)',
                flexShrink: 0,
                boxShadow: '0 0 0 2px rgba(59, 131, 246, 0.15)',
                transition: 'all 0.2s ease',
              }}
            />
            {needsTruncation ? (
              <CustomTooltip
                title={<Box sx={{ maxHeight: TOOLTIP_MAX_HEIGHT, overflow: 'auto', fontSize: 'var(--ds-text-small)', lineHeight: 1.5 }}>{text}</Box>}
                placement='bottom'
                tooltipStyle={{ maxWidth: '550px' }}
              >
                <Typography
                  sx={{
                    color: 'var(--ds-brand-500)',
                    paddingLeft: 'var(--ds-space-1)',
                    fontSize: 'var(--ds-text-body)',
                    cursor: 'pointer',
                    wordBreak: 'break-all',
                  }}
                  onClick={() => onItemClick?.(text)}
                >
                  {displayText}
                </Typography>
              </CustomTooltip>
            ) : (
              <Typography
                sx={{
                  color: 'var(--ds-brand-500)',
                  paddingLeft: 'var(--ds-space-1)',
                  fontSize: 'var(--ds-text-body)',
                  cursor: 'pointer',
                  wordBreak: 'break-all',
                }}
                onClick={() => onItemClick?.(text)}
              >
                {text}
              </Typography>
            )}
          </ListItem>
        );
      })}

      {data.length > initialCount && (
        <ListItem alignItems='center' sx={{ p: '0px 0px var(--ds-space-1) var(--ds-space-4)', cursor: 'pointer' }} onClick={toggleShowAll}>
          <Typography
            sx={{
              color: 'var(--ds-blue-500)',
              fontSize: 'var(--ds-text-small)',
            }}
          >
            {showAll ? 'Show less' : `Show more (${data.length - initialCount})`}
          </Typography>
        </ListItem>
      )}
      <Divider />
    </List>
  );
};

export default ShowMoreList;
