import CustomTable2 from '@components1/common/tables/CustomTable2';

import PropTypes from 'prop-types';

const AccountList = ({
  data = [],
  headers = [],
  upperHeaders = [],
  expandable = {},
  rowsPerPage,
  totalRows,
  onPageChange = null,
  sort = null,
  onSortChange = null,
}) => {
  function expandedComponentFn() {
    return <>No Data</>;
  }

  expandable?.tabs?.forEach((tab) => {
    tab.componentFn = tab.componentFn || expandedComponentFn;
  });
  return (
    <CustomTable2
      tableData={data}
      headers={headers}
      upperHeaders={upperHeaders}
      expandable={{ ...expandable }}
      rowsPerPage={rowsPerPage}
      onPageChange={onPageChange}
      sort={sort}
      onSortChange={onSortChange}
      totalRows={totalRows || data?.length}
    />
  );
};
export default AccountList;

AccountList.propTypes = {
  data: PropTypes.array,
  headers: PropTypes.array,
  upperHeaders: PropTypes.array,
  expandable: PropTypes.object,
  rowsPerPage: PropTypes.number,
  totalRows: PropTypes.number,
  onPageChange: PropTypes.func,
  sort: PropTypes.object,
  onSortChange: PropTypes.func,
};
