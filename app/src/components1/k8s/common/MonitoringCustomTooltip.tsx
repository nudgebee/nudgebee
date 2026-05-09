import { Table, TableBody, TableCell, TableHead, TableRow, Typography } from '@mui/material';
import Tooltip, { tooltipClasses, type TooltipProps } from '@mui/material/Tooltip';
import { styled } from '@mui/material/styles';

interface CustomTooltipProps {
  children: any;
  rows: any;
  type: string;
}

const CustomTooltip = styled(({ className, ...props }: TooltipProps) => <Tooltip {...props} classes={{ popper: className }} />)(({ theme }) => ({
  [`& .${tooltipClasses.tooltip}`]: {
    backgroundColor: '#FFFFFF',
    color: 'rgba(0, 0, 0, 0.87)',
    fontSize: theme.typography.pxToRem(12),
    border: '0.5px solid #93C5FD',
    boxShadow: '0px 4px 10px 0px #89899340',
    borderRadius: '4px',
    padding: '2px 12px',
    minWidth: '200px',
  },
}));

const MonitoringCustomTooltip = ({ children, rows, type }: CustomTooltipProps) => {
  return (
    <CustomTooltip
      placement='bottom-start'
      slotProps={{
        popper: {
          modifiers: [
            {
              name: 'offset',
              options: {
                offset: [90, -14],
              },
            },
          ],
        },
      }}
      title={
        <Table
          sx={{
            th: {
              '&:first-child': {
                fontWeight: 400,
                color: '#737373',
              },
              fontSize: '12px',
              fontWeight: 500,
              padding: '8px',
            },
            td: {
              '&:first-child': {
                fontSize: '12px',
                fontWeight: 400,
                color: '#737373',
              },
              fontSize: '13px',
              padding: '8px',
              color: '#374151',
            },
            '& span': {
              fontSize: '10px',
              fontWeight: 400,
              color: '#737373',
            },
          }}
        >
          <TableHead>
            <TableRow>
              <TableCell>Metrics</TableCell>
              <TableCell align='right'>
                {type === 'memory' ? (
                  <>
                    Memory <Typography component='span' />
                  </>
                ) : (
                  <>
                    CPU <Typography component='span'>(m)</Typography>
                  </>
                )}
              </TableCell>
            </TableRow>
          </TableHead>
          <TableBody>
            {rows.map((row: any) => (
              <TableRow key={row.matrics} sx={{ '&:last-child td, &:last-child th': { border: 0 } }}>
                <TableCell>{row.matrics}</TableCell>
                <TableCell align='right'>{row.data}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      }
    >
      {children}
    </CustomTooltip>
  );
};

export default MonitoringCustomTooltip;
