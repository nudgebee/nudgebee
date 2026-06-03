import React from 'react';
import { Box, List, ListItem, Typography } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import { ds } from '@utils/colors';

const NotificationCard = ({ data, key }) => {
  return (
    <Box
      sx={{
        borderRadius: 'var(--ds-radius-sm)',
        border: '1px solid var(--ds-gray-200)',
        backgroundColor: 'var(--ds-background-100)',
        minHeight: '100%',
        display: 'flex',
        flexDirection: 'column',
      }}
      key={key}
    >
      <Box>
        <Typography
          display={'flex'}
          alignItems={'center'}
          gap={ds.space[2]}
          fontWeight={500}
          fontSize={ds.text.bodyLg}
          color={ds.gray[600]}
          borderBottom={`1px solid ${ds.gray[200]}`}
          p={ds.space.mul(0, 5)}
        >
          <SafeIcon src={data.icon} alt={data.name} />
          {data.name}
        </Typography>
        <List>
          {data.notificationList.map((notification, _index) => (
            <ListItem
              key={notification.id}
              sx={{
                padding: 'var(--ds-space-1) var(--ds-space-4) var(--ds-space-1) var(--ds-space-5)',
                '&::before': {
                  content: `''`,
                  height: ds.space[1],
                  width: ds.space[1],
                  backgroundColor: 'var(--ds-brand-500)',
                  borderRadius: ds.radius.pill,
                  mr: 'var(--ds-space-2)',
                },
                color: 'var(--ds-brand-500)',
              }}
            >
              <Typography>{notification}</Typography>
            </ListItem>
          ))}
        </List>
      </Box>
    </Box>
  );
};

export default NotificationCard;
