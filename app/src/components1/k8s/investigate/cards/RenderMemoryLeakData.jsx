import { Typography, Box } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import LeakSuspended1 from '@assets/leak-suspected-1.svg';
import LeakSuspended2 from '@assets/leak-suspected-2.svg';
import LeakUsSuspended1 from '@assets/leak-unsuspected-1.svg';
import LeakUsSuspended2 from '@assets/leak-unsuspected-2.svg';

const RenderMemoryLeakData = () => {
  return (
    <Box sx={{ borderRadius: 'var(--ds-radius-lg)', padding: '0px var(--ds-space-4)' }}>
      <Box display={'flex'}>
        <Box
          sx={{
            marginRight: 'var(--ds-space-4)',
            border: '1px solid var(--ds-brand-200)',
            padding: 'var(--ds-space-4) var(--ds-space-3)',
            borderRadius: 'var(--ds-radius-md)',
          }}
        >
          {' '}
          <Typography sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-body-lg)', marginBottom: 'var(--ds-space-2)' }}>
            Memory Leak
          </Typography>
          <SafeIcon src={LeakSuspended1} alt='' />
          <SafeIcon style={{ marginLeft: 'var(--ds-space-4)' }} src={LeakSuspended2} alt='' />
        </Box>

        <Box sx={{ border: '1px solid var(--ds-brand-200)', padding: 'var(--ds-space-4) var(--ds-space-3)', borderRadius: 'var(--ds-radius-md)' }}>
          <Typography sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-body-lg)', marginBottom: 'var(--ds-space-2)' }}>No Leak</Typography>
          <SafeIcon src={LeakUsSuspended1} alt='' />
          <SafeIcon style={{ marginLeft: 'var(--ds-space-4)' }} src={LeakUsSuspended2} alt='' />
        </Box>
      </Box>
      <Box sx={{ marginTop: 'var(--ds-space-3)' }}>
        <Typography sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-body)' }}>
          1. A memory graph with continuously increasing pattern can indicate a memory leak
        </Typography>
        <Typography sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-body)' }}>
          2. If the application demand(i.e user traffic) has been gradually increasing, this may rule-out a memory leak and instead point to the need
          to increase the memory request/limit
        </Typography>
      </Box>
    </Box>
  );
};
RenderMemoryLeakData.propTypes = {};

export default RenderMemoryLeakData;
