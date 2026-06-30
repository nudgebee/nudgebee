import { createContext, useCallback, useContext, useMemo, useState } from 'react';

const DataContext = createContext();

let globalClusterData = [];

export const DataProvider = ({ children }) => {
  const [podLog, setPodLog] = useState({
    accountId: '',
    query: {},
  });
  const [selectedCluster, setSelectedCluster] = useState({});
  const [autoOptimizeName, setAutoOptimizeName] = useState('');
  const [allCluster, setAllCluster] = useState(null);
  const [providerCapabilities, setProviderCapabilities] = useState([]);

  // Stable references so the useMemo below only recomputes when state changes,
  // not on every parent re-render (e.g. route navigation). 47 components consume this context.
  const setAutoOptimizeNameRequest = useCallback((name) => {
    setAutoOptimizeName(name);
  }, []);

  const setPodLogRequest = useCallback((accountId, query) => {
    setPodLog((prevPodLog) => {
      if (prevPodLog.accountId === accountId && prevPodLog.query === query) {
        return prevPodLog;
      }
      return { ...prevPodLog, accountId, query };
    });
  }, []);

  const setAllClusterGlobal = useCallback((clusters) => {
    globalClusterData = clusters;
    setAllCluster(clusters);
  }, []);

  const value = useMemo(
    () => ({
      autoOptimizeName,
      setAutoOptimizeNameRequest,
      podLog,
      setPodLogRequest,
      selectedCluster,
      setSelectedCluster,
      setAllCluster: setAllClusterGlobal,
      allCluster,
      providerCapabilities,
      setProviderCapabilities,
    }),
    [
      autoOptimizeName,
      setAutoOptimizeNameRequest,
      podLog,
      setPodLogRequest,
      selectedCluster,
      setSelectedCluster,
      setAllClusterGlobal,
      allCluster,
      providerCapabilities,
      setProviderCapabilities,
    ]
  );

  return <DataContext.Provider value={value}>{children}</DataContext.Provider>;
};

export const useData = () => {
  return useContext(DataContext);
};

export function getClusterData(id) {
  if (globalClusterData && globalClusterData.length > 0) {
    return globalClusterData.find((item) => item.value === id);
  }
  return null;
}
