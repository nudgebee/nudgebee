import React from 'react';
import { Box, List, ListItem, Typography } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';

const NotificationCard = ({ data, key }) => {
  return (
    <Box
      sx={{
        borderRadius: '4px',
        border: '1px solid #EBEBEB',
        backgroundColor: '#FFFFFF',
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
          gap={1}
          fontWeight={500}
          fontSize={'14px'}
          color={'#737373'}
          borderBottom={'1px solid #EBEBEB'}
          p={'10px'}
        >
          <SafeIcon src={data.icon} alt={data.name} />
          {data.name}
        </Typography>
        <List>
          {data.notificationList.map((notification, _index) => (
            <ListItem
              key={notification.id}
              sx={{
                padding: '5px 15px 5px 24px',
                '&::before': {
                  content: `''`,
                  height: '4px',
                  width: '4px',
                  backgroundColor: '#374151',
                  borderRadius: '50%',
                  mr: '10px',
                },
                color: '#374151',
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
