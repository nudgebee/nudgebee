import * as React from 'react';
import PropTypes from 'prop-types';
import CustomIconButton from '@components1/CustomIconButton';
import { History } from '@mui/icons-material';
import BoxLayout2 from './BoxLayout2';
import CustomTable from './tables/CustomTable2';
import { Modal } from '@components1/common/modal';
import apiUser from '@api1/user';
import CopyableText from './CopyableText';
import Text from './format/Text';
import Datetime from './format/Datetime';
import { Box } from '@mui/material';
import { safeJSONParse } from 'src/utils/common';

export function UserHistory({ accountId, module, onCopyClick }) {
  const headers = [{ name: 'Query', width: '70%' }, 'Executed At', 'Status'];

  const [historyData, setHistoryData] = React.useState([]);
  const [limit, setLimit] = React.useState(apiUser.getUserPreferencesTablePageSize());
  const [page, setPage] = React.useState(1);
  const [loading, setLoading] = React.useState(false);

  React.useEffect(() => {
    if (!accountId) {
      return;
    }
    if (!module) {
      return;
    }
    const offset = limit * (page - 1);
    setHistoryData([]);
    setLoading(true);
    apiUser
      .getHistory({ accountId, module, limit, offset })
      .then((res) => {
        let rows = res?.data?.user_history?.map((h) => {
          let response = [];
          let queries = '';
          try {
            const parsedQueries = safeJSONParse(h.data);
            if (parsedQueries?.length > 0) {
              queries = parsedQueries?.map((q) => q.query.replace('query=', '')).join('\n');
            } else {
              queries = h.data.replace('query=', '');
            }
          } catch {
            queries = h.data.replace('query=', '');
          }
          response.push({
            component: (
              <Box display='flex'>
                <CopyableText copyableText={queries} onCopy={onCopyClick} />
                <Text value={queries} sx={{ overflowWrap: 'anywhere' }} />
              </Box>
            ),
          });
          response.push({ component: <Datetime value={h.created_at} /> });
          response.push({ component: <Text value={h.status} /> });
          return response;
        });
        setHistoryData(rows);
      })
      .finally(() => {
        setLoading(false);
      });
  }, [accountId, module, limit, page]);

  return (
    <BoxLayout2
      id='userHistory'
      sx={{
        padding: '16px 14px 20px 14px',
        alignSelf: 'stretch',
        backgroundColor: 'white',
        '@media (max-width: 1350px)': {
          padding: '16px 8px 20px 8px',
        },
        minHeight: '400px',
      }}
      sharingOptions={{
        sharing: {
          enabled: false,
          onClick: null,
        },
        download: {
          enabled: false,
          onClick: () => {
            return {
              tableId: '',
            };
          },
        },
      }}
    >
      <CustomTable
        headers={headers}
        tableData={historyData}
        pageNumber={page}
        rowsPerPage={limit}
        onPageChange={(p, l) => {
          setPage(p);
          setLimit(l);
        }}
        loading={loading}
      />
    </BoxLayout2>
  );
}

UserHistory.propTypes = {
  accountId: PropTypes.string.isRequired,
  module: PropTypes.string.isRequired,
  onCopyClick: PropTypes.func,
};

export function UserHistoryPopup({ accountId, module, onCopyClick, isOpen, onClose }) {
  return (
    <Modal
      width='lg'
      open={isOpen}
      handleClose={() => {
        if (onClose) {
          onClose();
        }
      }}
      title={'History'}
      sx={{
        '& .MuiPaper-root': {
          maxWidth: '1010px',
          '& .MuiDialogContent-root': {
            padding: '16px 40px',
          },
        },
        height: '700px',
      }}
    >
      <UserHistory accountId={accountId} module={module} onCopyClick={onCopyClick} />
    </Modal>
  );
}

UserHistoryPopup.propTypes = {
  accountId: PropTypes.string.isRequired,
  module: PropTypes.string.isRequired,
  isOpen: PropTypes.bool.isRequired,
  onCopyClick: PropTypes.func,
  onClose: PropTypes.func,
};

export default function UserHistoryButton({ accountId, module, onCopyClick }) {
  const [isPopupOpen, setIsPopupOpen] = React.useState(false);

  const onButtonClick = () => {
    setIsPopupOpen(true);
  };

  return (
    <>
      <UserHistoryPopup
        isOpen={isPopupOpen}
        accountId={accountId}
        module={module}
        onCopyClick={onCopyClick}
        onClose={(_e) => {
          setIsPopupOpen(false);
        }}
      />
      <CustomIconButton variant='iconButton' onClick={onButtonClick}>
        <History />
      </CustomIconButton>
    </>
  );
}

UserHistoryButton.propTypes = {
  accountId: PropTypes.string.isRequired,
  module: PropTypes.string.isRequired,
  onCopyClick: PropTypes.func,
};
