import React, { useContext, useState, useMemo, useCallback, createContext } from 'react';
import { getStartOfMonth, getEndOfMonth } from '@lib/datetime';

const currentDate = new Date();

const globalFilter = {
  startDate: getStartOfMonth(currentDate),
  endDate: getEndOfMonth(currentDate),
  setStartDate: function (date: Date) {
    globalFilter.startDate = date;
  },
  setEndDate: function (date: Date) {
    globalFilter.endDate = date;
  },
};

export function getGlobalStartDate(): Date {
  return globalFilter.startDate;
}

export function getGlobalEndDate(): Date {
  return globalFilter.endDate;
}

export const GlobalFilterContxt = createContext({
  startDate: globalFilter.startDate,
  endDate: globalFilter.endDate,
  setStartDate: globalFilter.setStartDate,
  setEndDate: globalFilter.setEndDate,
});

export const useGlobalFilter = () => useContext(GlobalFilterContxt);

export const GlobalFilterContextProvider = ({ children }: any) => {
  const [startDate, _setStartDate] = useState(globalFilter.startDate);
  const [endDate, _setEndDate] = useState(globalFilter.endDate);

  const setEndDate = useCallback((d: Date | any) => {
    if (d.toDate) {
      d = d.toDate();
    }
    d.setHours(23, 59, 59, 999);
    _setEndDate(d);
    globalFilter.setEndDate(d);
  }, []);

  const setStartDate = useCallback((d: Date | any) => {
    if (d.toDate) {
      d = d.toDate();
    }
    d.setHours(0, 0, 0, 0);
    _setStartDate(d);
    globalFilter.setStartDate(d);
  }, []);

  const value = useMemo(() => ({ startDate, endDate, setStartDate, setEndDate }), [startDate, endDate, setStartDate, setEndDate]);

  return <GlobalFilterContxt.Provider value={value}>{children}</GlobalFilterContxt.Provider>;
};
