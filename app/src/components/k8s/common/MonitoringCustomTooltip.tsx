import { Table, TableBody, TableCell, TableHead, TableRow, Typography } from '@mui/material';
import Tooltip, { tooltipClasses, type TooltipProps } from '@mui/material/Tooltip';
import { styled } from '@mui/material/styles';
import { ds } from '@utils/colors';

interface CustomTooltipProps {
  children: any;
  rows: any;
  type: string;
}

const CustomTooltip = styled(({ className, ...props }: TooltipProps) => <Tooltip {...props} classes={{ popper: className }} />)(({ theme }) => ({
  [`& .${tooltipClasses.tooltip}`]: {
    backgroundColor: 'var(--ds-background-100)',
    color: 'rgba(0, 0, 0, 0.87)',
    fontSize: theme.typography.pxToRem(12),
    border: '0.5px solid var(--ds-blue-300)',
    boxShadow: '0px 4px 10px 0px #89899340',
    borderRadius: 'var(--ds-radius-sm)',
    padding: 'var(--ds-space-1) var(--ds-space-3)',
    minWidth: ds.space.mul(0, 100),
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
                fontWeight: 'var(--ds-font-weight-regular)',
                color: 'var(--ds-gray-600)',
              },
              fontSize: 'var(--ds-text-small)',
              fontWeight: 'var(--ds-font-weight-medium)',
              padding: 'var(--ds-space-2)',
            },
            td: {
              '&:first-child': {
                fontSize: 'var(--ds-text-small)',
                fontWeight: 'var(--ds-font-weight-regular)',
                color: 'var(--ds-gray-600)',
              },
              fontSize: 'var(--ds-text-body)',
              padding: 'var(--ds-space-2)',
              color: 'var(--ds-brand-500)',
            },
            '& span': {
              fontSize: 'var(--ds-text-caption)',
              fontWeight: 'var(--ds-font-weight-regular)',
              color: 'var(--ds-gray-600)',
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
