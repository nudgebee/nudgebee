import * as React from 'react';
import PropTypes from 'prop-types';
import { Button } from '@ui/Button';
import { History } from '@mui/icons-material';
import { ListingLayout } from '@ui/ListingLayout';
import CustomTable from '@shared/tables/CustomTable';
import { Modal } from '@ui/Modal';
import apiUser from '@api1/user';
import CopyButton from '@shared/buttons/CopyButton';
import { Label } from '@ui/Label';
import Text from '@shared/format/Text';
import Datetime from '@shared/format/Datetime';
import { Box } from '@mui/material';
import { safeJSONParse } from 'src/utils/common';
import { ds } from '@utils/colors';

export function UserHistory({ accountId, module }) {
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
        let rows = res?.data?.users_create_history?.map((h) => {
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
                <CopyButton text={queries} size='sm' />
                <Text value={queries} sx={{ overflowWrap: 'anywhere' }} />
              </Box>
            ),
          });
          response.push({ component: <Datetime value={h.created_at} /> });
          response.push({ component: <Label text={h.status} /> });
          return response;
        });
        setHistoryData(rows);
      })
      .finally(() => {
        setLoading(false);
      });
  }, [accountId, module, limit, page]);

  return (
    <ListingLayout
      id='userHistory'
      sx={{
        alignSelf: 'stretch',
        backgroundColor: 'white',
        minHeight: ds.space.mul(0, 200),
      }}
    >
      <ListingLayout.Body
        padding={`${ds.space[4]} ${ds.space.mul(0, 7)} ${ds.space.mul(0, 10)} ${ds.space.mul(0, 7)}`}
        sx={{ '@media (max-width: 1350px)': { padding: 'var(--ds-space-4) var(--ds-space-2) var(--ds-space-4) var(--ds-space-2)' } }}
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
          tableHeadingCenter={['Executed At']}
          loading={loading}
        />
      </ListingLayout.Body>
    </ListingLayout>
  );
}

UserHistory.propTypes = {
  accountId: PropTypes.string.isRequired,
  module: PropTypes.string.isRequired,
};

export function UserHistoryPopup({ accountId, module, isOpen, onClose }) {
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
          maxWidth: ds.space.mul(0, 505),
          '& .MuiDialogContent-root': {
            padding: 'var(--ds-space-4) var(--ds-space-6)',
          },
        },
        height: ds.space.mul(0, 350),
      }}
    >
      <UserHistory accountId={accountId} module={module} />
    </Modal>
  );
}

UserHistoryPopup.propTypes = {
  accountId: PropTypes.string.isRequired,
  module: PropTypes.string.isRequired,
  isOpen: PropTypes.bool.isRequired,
  onClose: PropTypes.func,
};

export default function UserHistoryButton({ accountId, module }) {
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
        onClose={(_e) => {
          setIsPopupOpen(false);
        }}
      />
      <Button tone='secondary' composition='icon-only' icon={<History />} aria-label='History' tooltip='History' onClick={onButtonClick} />
    </>
  );
}

UserHistoryButton.propTypes = {
  accountId: PropTypes.string.isRequired,
  module: PropTypes.string.isRequired,
};
