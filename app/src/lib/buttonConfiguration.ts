const buttonConfiguration = {
  buttonConfigs: {
    buttonsAlgo: [
      { id: 0, label: 'NB Algo', value: 'NBALGO' },
      { id: 1, label: 'P99', value: 'P99' },
      { id: 2, label: 'P97', value: 'P97' },
      { id: 3, label: 'P95', value: 'P95' },
    ],
    buttonsBuffer: [
      { id: 0, label: 'No buffer', value: 0 },
      { id: 1, label: '+5% buffer', value: 5 },
      { id: 2, label: '+10% buffer', value: 10 },
      { id: 3, label: '+15% buffer', value: 15 },
    ],
    buttonMemoryAlgo: [{ id: 0, label: 'NB Algo', value: 0 }],
    buttonMemoryBuffer: [
      { id: 0, label: 'No buffer', value: 0 },
      { id: 1, label: '+5% buffer', value: 5 },
      { id: 3, label: '+10% buffer', value: 10 },
      { id: 4, label: '+15% buffer', value: 15 },
      { id: 5, label: '+20% buffer', value: 20 },
    ],
    buttonAutoPilotFor: [
      { id: 0, label: 'Right sizing' },
      { id: 1, label: 'Right sizing  with bin packing' },
      { id: 2, label: 'Pvc Rightsizing' },
      { id: 3, label: 'Abandoned Workloads' },
      { id: 4, label: 'Unused Volumes' },
    ],
  },
  timeButtonConfigs: {
    timeFrame: [
      { id: 0, label: 'Daily', value: 'Daily' },
      { id: 1, label: 'Weekly', value: 'Weekly' },
      { id: 2, label: 'Monthly', value: 'Monthly' },
      { id: 3, label: 'Cron Expression', value: 'Cron Expression' },
    ],
    daily: Array.from({ length: 24 }, (_, index) => ({
      id: `hour${index}`,
      label: `${index % 12 === 0 ? 12 : index % 12}:00 ${index < 12 ? 'AM' : 'PM'}`,
    })),
    weekly: [
      { id: 'mon', label: 'Mon' },
      { id: 'tue', label: 'Tue' },
      { id: 'wed', label: 'Wed' },
      { id: 'thu', label: 'Thur' },
      { id: 'fri', label: 'Fri' },
      { id: 'sat', label: 'Sat' },
      { id: 'sun', label: 'Sun' },
    ],
  },
};
export default buttonConfiguration;
