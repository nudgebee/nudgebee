import { useState } from 'react';
import { Box, Tabs, Tab } from '@mui/material';
import OptimizeNewPage from '@components1/optimise-new/OptimizeNewPage';
import SummaryView from '@components1/optimise-new/summary/SummaryView';

const TAB_LABELS = { summary: 'Summary', recommendations: 'Recommendations' };

const Optimise = () => {
  const [activeTab, setActiveTab] = useState(0);

  return (
    <Box>
      <Tabs
        value={activeTab}
        onChange={(_, v) => setActiveTab(v)}
        data-testid='optimize-tabs'
        sx={{
          minHeight: '36px',
          borderBottom: '1px solid #E5E7EB',
          mb: '4px',
          '& .MuiTabs-indicator': { backgroundColor: '#2563EB', height: '2px' },
        }}
      >
        <Tab
          label={TAB_LABELS.summary}
          data-testid='tab-summary'
          sx={{
            fontSize: '13px',
            textTransform: 'none',
            minHeight: '36px',
            px: '16px',
            fontWeight: activeTab === 0 ? 700 : 400,
            color: activeTab === 0 ? '#2563EB' : '#6B7280',
            '&.Mui-selected': { color: '#2563EB' },
          }}
        />
        <Tab
          label={TAB_LABELS.recommendations}
          data-testid='tab-recommendations'
          sx={{
            fontSize: '13px',
            textTransform: 'none',
            minHeight: '36px',
            px: '16px',
            fontWeight: activeTab === 1 ? 700 : 400,
            color: activeTab === 1 ? '#2563EB' : '#6B7280',
            '&.Mui-selected': { color: '#2563EB' },
          }}
        />
      </Tabs>

      {activeTab === 0 && <SummaryView />}
      {activeTab === 1 && <OptimizeNewPage />}
    </Box>
  );
};

export default Optimise;
