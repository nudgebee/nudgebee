import { Typography, Box } from '@mui/material';
import SafeIcon from '@components1/common/SafeIcon';
import LeakSuspended1 from '@assets/leak-suspected-1.svg';
import LeakSuspended2 from '@assets/leak-suspected-2.svg';
import LeakUsSuspended1 from '@assets/leak-unsuspected-1.svg';
import LeakUsSuspended2 from '@assets/leak-unsuspected-2.svg';

const RenderMemoryLeakData = () => {
  return (
    <Box sx={{ borderRadius: '8px', padding: '0px 16px' }}>
      <Box display={'flex'}>
        <Box sx={{ marginRight: '16px', border: '1px solid #D0D0D0', padding: '16px 12px', borderRadius: '6px' }}>
          {' '}
          <Typography sx={{ color: '#7c7979', fontSize: '14px', marginBottom: '10px' }}>Memory Leak</Typography>
          <SafeIcon src={LeakSuspended1} alt='' />
          <SafeIcon style={{ marginLeft: '20px' }} src={LeakSuspended2} alt='' />
        </Box>

        <Box sx={{ border: '1px solid #D0D0D0', padding: '16px 12px', borderRadius: '6px' }}>
          <Typography sx={{ color: '#7c7979', fontSize: '14px', marginBottom: '10px' }}>No Leak</Typography>
          <SafeIcon src={LeakUsSuspended1} alt='' />
          <SafeIcon style={{ marginLeft: '20px' }} src={LeakUsSuspended2} alt='' />
        </Box>
      </Box>
      <Box sx={{ marginTop: '12px' }}>
        <Typography sx={{ color: '#7c7979', fontSize: '13px' }}>
          1. A memory graph with continuously increasing pattern can indicate a memory leak
        </Typography>
        <Typography sx={{ color: '#7c7979', fontSize: '13px' }}>
          2. If the application demand(i.e user traffic) has been gradually increasing, this may rule-out a memory leak and instead point to the need
          to increase the memory request/limit
        </Typography>
      </Box>
    </Box>
  );
};
RenderMemoryLeakData.propTypes = {};

export default RenderMemoryLeakData;
