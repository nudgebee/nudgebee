import React, { useEffect, useState } from 'react';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { useRouter } from 'next/router';
import PropTypes from 'prop-types';
import { ds } from '@utils/colors';

/**
 * @param {{
 *   data?: any[],
 *   headers?: any[],
 *   upperHeaders?: any[],
 *   expandable?: object,
 *   rowsPerPage?: number,
 *   totalRows?: number,
 *   onPageChange?: (page: number, limit: number) => void,
 *   sort?: object,
 *   onSortChange?: any,
 *   id?: string,
 *   showExpandable?: boolean,
 *   loading?: boolean,
 *   errorMessage?: string,
 *   pageNumber?: number,
 *   _selectedDateRange?: object,
 *   rounded?: any,
 *   borderColor?: string,
 *   minWidth?: string | number,
 *   textAlign?: string,
 *   timeStampMinWidth?: boolean,
 *   tableHeadingCenter?: string | any[],
 *   stickyColumnIndex?: string | number,
 *   showUpdatedTable?: boolean,
 * }} props
 */

const CloudAccountTable = ({
  data = [],
  headers = [],
  upperHeaders = [],
  expandable = {},
  rowsPerPage = 5,
  totalRows,
  onPageChange,
  sort = {},
  onSortChange,
  id = '',
  showExpandable = false,
  loading = false,
  errorMessage = '',
  pageNumber = 1,
  _selectedDateRange = {},
  rounded,
  borderColor = ds.gray[300],
  minWidth,
  textAlign = '',
  timeStampMinWidth = false,
  tableHeadingCenter = '',
  stickyColumnIndex = '',
  showUpdatedTable = false,
}) => {
  const router = useRouter();
  const [accountId, setAccountId] = useState(router.query.KubernetesDetails || router.query.accountId);
  const [requiredTabs, setRequiredTabs] = useState({});

  useEffect(() => {
    setAccountId(router.query.KubernetesDetails || router.query.accountId);
  }, [router.query.KubernetesDetails, router.query.accountId]);

  function expandedComponentFn(_option, _query, _row) {
    return <>No Data</>;
  }

  function wrapperFunction(fn) {
    return (option, query, row) => fn(accountId, query, row);
  }

  const checkForTabsWithData = (_rowData) => {
    let tabs = [];
    let idx = -1;
    if (expandable) {
      if (expandable.tabs && expandable.tabs.length > 0) {
        for (const tab of expandable.tabs) {
          idx = idx + 1;
          tabs.push({
            text: tab.text,
            value: idx,
            key: tab.key,
            componentFn: tab.componentFn ? wrapperFunction(tab.componentFn) : expandedComponentFn,
          });
        }
      }
    }
    setRequiredTabs({
      tabs: tabs,
    });
  };

  return (
    <CustomTable2
      id={id}
      tableData={data}
      headers={headers}
      upperHeaders={upperHeaders}
      expandable={requiredTabs}
      rowsPerPage={rowsPerPage}
      onPageChange={onPageChange}
      sort={sort}
      onSortChange={onSortChange}
      totalRows={totalRows || data?.length}
      checkForTabsWithData={checkForTabsWithData}
      showExpandable={showExpandable}
      loading={loading}
      errorMessage={errorMessage}
      pageNumber={pageNumber}
      rounded={rounded}
      borderColor={borderColor}
      minWidth={minWidth}
      textAlign={textAlign}
      tableHeadingCenter={tableHeadingCenter}
      timeStampMinWidth={timeStampMinWidth}
      stickyColumnIndex={stickyColumnIndex}
      showUpdatedTable={showUpdatedTable}
    />
  );
};

export default CloudAccountTable;

CloudAccountTable.propTypes = {
  id: PropTypes.string,
  data: PropTypes.array,
  headers: PropTypes.array,
  upperHeaders: PropTypes.array,
  expandable: PropTypes.object,
  rowsPerPage: PropTypes.number,
  totalRows: PropTypes.number,
  onPageChange: PropTypes.func,
  sort: PropTypes.object,
  onSortChange: PropTypes.any,
  showExpandable: PropTypes.bool,
  loading: PropTypes.bool,
  errorMessage: PropTypes.string,
  pageNumber: PropTypes.number,
  selectedDateRange: PropTypes.object,
  rounded: PropTypes.any,
  minWidth: PropTypes.any,
  textAlign: PropTypes.string,
  timeStampMinWidth: PropTypes.bool,
  borderColor: PropTypes.string,
  stickyColumnIndex: PropTypes.any,
  showUpdatedTable: PropTypes.bool,
};
