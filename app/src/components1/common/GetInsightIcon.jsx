import { CpuRedIcon, InfoRedIcon, MemoryRedIcon, WrenchIcon } from '@assets';

export const GetInsightIcon = (item) => {
  switch (item.source) {
    case 'Event':
      return InfoRedIcon;
    case 'Recommendation':
      return WrenchIcon;
    case 'Metric':
      switch (true) {
        case item.title.toLowerCase().includes('memory'):
          return MemoryRedIcon;
        case item.title.toLowerCase().includes('cpu'):
          return CpuRedIcon;
        default:
          return WrenchIcon;
      }
    default:
      return WrenchIcon;
  }
};
