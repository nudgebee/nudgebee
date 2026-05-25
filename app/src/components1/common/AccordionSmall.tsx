import React, { useState, type ReactNode } from 'react';
import { Accordion, AccordionSummary, AccordionDetails, Typography, Box, Select, MenuItem, FormControl, type SelectChangeEvent } from '@mui/material';
import ExpandMoreIcon from '@mui/icons-material/ExpandMore';
import { colors } from 'src/utils/colors';
import CustomLabels from './widgets/CustomLabels';

interface StatusOption {
  value: string;
  label: string;
  color: string;
}

interface AccordionSmallProps {
  children: ReactNode;
  header: ReactNode | string;
  status?: string;
  enableStatusDropdown?: boolean;
  onStatusChange?: (status: string) => void;
  currentStatus?: string;
  summarySx?: Record<string, any>;
  expanded?: boolean;
  onExpandedChange?: (expanded: boolean) => void;
}

const statusOptions: StatusOption[] = [
  { value: 'pending', label: 'PENDING', color: '#6B7280' },
  { value: 'skipped', label: 'SKIPPED', color: '#F59E0B' },
  { value: 'completed', label: 'COMPLETED', color: '#10B981' },
  { value: 'failed', label: 'FAILED', color: '#EF4444' },
];

const AccordionSmall: React.FC<AccordionSmallProps> = ({
  children,
  header,
  status,
  enableStatusDropdown = false,
  onStatusChange,
  currentStatus,
  summarySx = {},
  expanded: controlledExpanded,
  onExpandedChange,
}) => {
  const [internalExpanded, setInternalExpanded] = useState(false);
  const expanded = controlledExpanded !== undefined ? controlledExpanded : internalExpanded;
  const [internalStatus, setInternalStatus] = useState(currentStatus || status);

  const handleStatusChange = (event: SelectChangeEvent<string>) => {
    const newStatus = event.target.value as string;
    setInternalStatus(newStatus);
    if (onStatusChange) {
      onStatusChange(newStatus);
    }
  };

  return (
    <>
      <Accordion
        expanded={expanded}
        onChange={(_, isExpanded) => {
          if (controlledExpanded !== undefined && onExpandedChange) {
            onExpandedChange(isExpanded);
          } else {
            setInternalExpanded(isExpanded);
          }
        }}
        sx={{
          background: 'transparent',
          border: `1px solid ${expanded ? colors.primary : colors.border.secondaryLight}`,
          borderRadius: '8px',
          boxShadow: 'none',
          transition: 'border-color 0.2s ease-in-out',
        }}
      >
        <AccordionSummary expandIcon={<ExpandMoreIcon />} sx={{ py: '8px', minHeight: '40px', ...summarySx }}>
          <Box sx={{ display: 'flex', flexDirection: 'row', width: '100%', justifyContent: 'space-between', alignItems: 'center' }}>
            {typeof header === 'string' ? (
              <Typography sx={{ color: colors.text.secondary, fontSize: '12px', fontWeight: 400, width: '100%' }}>{header}</Typography>
            ) : (
              header
            )}
            {enableStatusDropdown ? (
              <FormControl size='small' sx={{ minWidth: 90 }} onClick={(e) => e.stopPropagation()}>
                <Select
                  value={internalStatus}
                  onChange={handleStatusChange}
                  renderValue={(value) => <CustomLabels text={value} height='20px' />}
                  sx={{
                    height: '20px',
                    '& .MuiOutlinedInput-notchedOutline': {
                      border: 'none',
                    },
                    '& .MuiSelect-select': {
                      p: 0,
                      display: 'flex',
                      alignItems: 'center',
                    },
                  }}
                >
                  {statusOptions.map((option) => (
                    <MenuItem
                      key={option.value}
                      value={option.value}
                      sx={{
                        p: 1,
                        justifyContent: 'center',
                      }}
                    >
                      <CustomLabels text={option.value} height='20px' />
                    </MenuItem>
                  ))}
                </Select>
              </FormControl>
            ) : (
              status && <CustomLabels text={status} />
            )}
          </Box>
        </AccordionSummary>
        <AccordionDetails sx={{ pt: '8px', pb: '12px' }}>{children}</AccordionDetails>
      </Accordion>
    </>
  );
};

export default AccordionSmall;
