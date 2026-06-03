import React, { useEffect, useState } from 'react';
import CustomDropdown from '@components1/common/CustomDropdown';
import { createFilterOptions } from '@mui/material/Autocomplete';
import { Box, Button, Grid, TextField, ToggleButton, ToggleButtonGroup, autocompleteClasses } from '@mui/material';
import AutoCompleteInput from '@components1/common/inputs/AutoCompleteInput';
import { ds } from 'src/utils/colors';
import CustomButton from '@components1/common/NewCustomButton';
import { DeleteIconRed } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { getOperatorsForKind, OperatorDescriptor, OperatorOption } from './operatorCatalog';

// Dynatrace metrics aggregator functions for BUILDER mode.
// These are aggregation functions, not filter operators — they stay hardcoded.
export const dynatraceMetricAggregators = [
  { label: 'avg', value: 'avg' },
  { label: 'min', value: 'min' },
  { label: 'max', value: 'max' },
  { label: 'sum', value: 'sum' },
  { label: 'count', value: 'count' },
];

// Line operators are now derived from backend-advertised supported_operator_descriptors.
export const getLineOperators = (operatorDescriptors?: OperatorDescriptor[]): OperatorOption[] => getOperatorsForKind(operatorDescriptors, 'line');
const dropDownSx = {
  m: '0 !important',
  p: '0 var(--ds-space-1) 0 !important',
  width: '-webkit-fill-available',
  minHeight: ds.space.mul(0, 15),
  border: '0px !important',
  borderRadius: 'var(--ds-radius-sm)',
  '&:hover': {
    boxShadow: 'unset',
  },
  '& .MuiFormControl-root': {
    m: 'var(--ds-space-1) 0 0px 0!important',
    p: '0 !important',
  },
  '& button': {
    m: '0 !important',
  },
  [`& .${autocompleteClasses.inputRoot}::before,  .${autocompleteClasses.input}::before, .${autocompleteClasses.inputRoot},  .${autocompleteClasses.input} `]:
    {
      border: '0px !important',
    },
};

const toggleBtnGrpSx = {
  textTransform: 'unset',
  boxShadow: 'unset',
  color: ds.gray[700],
  fontSize: '0.875rem',
  fontWeight: 'var(--ds-font-weight-medium)',
  width: '100%',
  height: ds.space[6],
  margin: '0 var(--ds-space-1) var(--ds-space-1) 0',
};
const toggleBtnSx = {
  m: '0 !important',
  p: 0,
  minWidth: ds.space.mul(0, 80),
};

const toggleIconBtnSx = {
  ml: '30 !important',
  color: ds.red[300],
  backgroundColor: ds.background[100],
  border: `1px solid ${ds.red[300]}`,
  '&:hover': {
    boxShadow: 'unset',
    backgroundColor: ds.brand[100],
  },
};

const primaryBtnSx = {
  top: ds.space[1],
  height: ds.space[6],
  m: '0 var(--ds-space-1) 0 0 !important',
  color: ds.background[100],
  backgroundColor: ds.background[100],
  '&:hover': {
    boxShadow: 'unset',
    backgroundColor: ds.brand[100],
  },
};

interface QueryBuilderProps {
  indexId: number;
  label: string;
  operator: string;
  value: string;
  removeFilter: boolean;
  labelOption: any;
  callback: any;
  logProvider?: string;
  operatorDescriptors?: OperatorDescriptor[];
}
const QueryBuilder: React.FC<QueryBuilderProps> = ({
  indexId,
  label,
  operator,
  value = '',
  labelOption,
  removeFilter,
  callback,
  logProvider,
  operatorDescriptors,
}: QueryBuilderProps) => {
  const chipOperators = getOperatorsForKind(operatorDescriptors, 'chip');
  const defaultOperator = chipOperators[0]?.value ?? '_eq';
  const [qLValueOption, setQLValueOption] = useState<string[]>(['']);

  useEffect(() => {
    if (label && label != '') {
      callback.fetchValueByLabel(label, setQLValueOption);
    }
  }, [label]);

  const filter = createFilterOptions<any>();
  return (
    <ToggleButtonGroup size='small' aria-label='text formatting' sx={{ ...toggleBtnGrpSx, width: 'auto' }}>
      <ToggleButton value='underlined' title={label} aria-label='color' sx={{ ...toggleBtnSx }}>
        <CustomDropdown
          options={labelOption.length != 0 ? labelOption : []}
          value={label ?? undefined}
          inputVariant='standard'
          customStyle={{ ...dropDownSx }}
          minWidth={ds.space.mul(0, 60)}
          label=''
          showBreakWord
          onChange={(_event, newValue) => {
            if (typeof newValue === 'string') {
              callback.addLabel({ target: { value: newValue } });
            } else if (newValue?.inputValue) {
              callback.addLabel({ target: { value: newValue.inputValue } });
            }
          }}
          additionalAutoCompleteProps={{
            filterOptions: (options: any, params: any) => {
              const filtered = filter(options, params);
              if (params.inputValue !== '') {
                filtered.push(params.inputValue);
              }
              return filtered;
            },
            getOptionLabel: (option: any) => {
              if (typeof option === 'string') {
                return option;
              }
              if (option.inputValue) {
                return option.inputValue;
              }
              return option.title;
            },
          }}
        />
      </ToggleButton>
      <ToggleButton value='color' aria-label='color' title={operator} sx={{ ...toggleBtnSx, minWidth: ds.space.mul(0, 35) }}>
        <CustomDropdown
          options={chipOperators}
          minWidth={ds.space.mul(0, 35)}
          label=''
          onChange={(e) => {
            callback.addOperator(e);
          }}
          value={operator ?? defaultOperator}
          inputVariant='standard'
          customStyle={{ ...dropDownSx, width: `${ds.space.mul(0, 25)} !important` }}
          additionalAutoCompleteProps={{ disableClearable: true }}
        />
      </ToggleButton>
      <ToggleButton
        value='color'
        aria-label='color'
        sx={{
          ...toggleBtnSx,
        }}
      >
        {logProvider === 'ES' && (
          <TextField
            value={value}
            placeholder='Enter text'
            id='standard-basic'
            sx={{
              width: '100%',
              pl: 'var(--ds-space-2)',
              '.MuiInput-root::before': {
                borderBottom: '0px',
              },
              '& .MuiInput-Input::focus': {
                border: '0px !important',
                outline: '0px !important',
              },
              '& .MuiInput-Input:hover': {
                border: '0px !important',
                outline: '0px !important',
              },
            }}
            onChange={(event) => {
              callback.addValue(event);
            }}
            variant='standard'
          />
        )}
        {logProvider === 'loki' && (
          <CustomDropdown
            options={qLValueOption.length ? qLValueOption : ['']}
            minWidth={ds.space.mul(0, 60)}
            label={''}
            onChange={(_event, newValue) => {
              if (typeof newValue === 'string') {
                callback.addValue({ target: { value: newValue } });
              } else if (newValue?.inputValue) {
                callback.addValue({ target: { value: newValue.inputValue } });
              }
            }}
            additionalAutoCompleteProps={{
              filterOptions: (options: any, params: any) => {
                const filtered = filter(options, params);
                if (params.inputValue !== '') {
                  filtered.push(params.inputValue);
                }
                return filtered;
              },
              getOptionLabel: (option: any) => {
                if (typeof option === 'string') {
                  return option;
                }
                if (option.inputValue) {
                  return option.inputValue;
                }
                return option.title;
              },
            }}
            value={value}
            inputVariant='standard'
            customStyle={{ ...dropDownSx }}
            componentsProps={{
              paper: {
                sx: {
                  minWidth: ds.space.mul(0, 60),
                  width: 'fit-content',
                },
              },
            }}
          />
        )}
      </ToggleButton>
      <Box sx={{ marginLeft: 'var(--ds-space-1)' }}>
        <CustomButton
          className='custom-delete-btn'
          variant='tertiary'
          onClick={() => {
            callback.removeLabelFilter(indexId);
          }}
          disabled={removeFilter}
          sx={{
            ...toggleIconBtnSx,
            border: '1px solid var(--ds-red-300) !important',
          }}
          startIcon={<SafeIcon src={DeleteIconRed} alt='delete' width={15} height={15} />}
        />
      </Box>
    </ToggleButtonGroup>
  );
};

interface OperationBuilderProps {
  index: number;
  lineContains: any;
  removeFilter: boolean;
  callback: any;
  operatorDescriptors?: OperatorDescriptor[];
  showBorder?: boolean;
  showMargin?: boolean;
  showPadding?: boolean;
}
export const OperationBuilder = ({
  index,
  lineContains,
  removeFilter,
  callback,
  operatorDescriptors,
  showBorder = true,
  showMargin = true,
  showPadding = true,
}: OperationBuilderProps) => {
  return (
    <Grid
      item
      sx={{
        ...(showBorder && { border: `1px solid ${ds.gray[500]}` }),
        ...(showMargin && { m: ds.space[1] }),
        ...(showPadding && { p: ds.space[2] }),
      }}
    >
      <ToggleButtonGroup size='small' aria-label='text formatting' sx={{ ...toggleBtnGrpSx, gap: 'var(--ds-space-1)' }}>
        <ToggleButton value='underlined' title={'label'} aria-label='color' sx={{ ...toggleBtnSx, width: '100%' }}>
          <CustomDropdown
            options={getLineOperators(operatorDescriptors)}
            minWidth={ds.space.mul(0, 35)}
            label=''
            onChange={(e) => {
              callback.addOperator(e);
            }}
            value={lineContains[index].operator}
            inputVariant='standard'
            customStyle={{ ...dropDownSx, width: 'inherit !important' }}
            additionalAutoCompleteProps={{
              disableClearable: true,
            }}
          />
        </ToggleButton>
        <CustomButton
          variant='tertiary'
          onClick={() => {
            callback.removeLabelFilter(index);
          }}
          disabled={removeFilter}
          sx={{
            ...toggleIconBtnSx,
            border: '1px solid var(--ds-red-300) !important',
          }}
          startIcon={<SafeIcon src={DeleteIconRed} alt='delete' width={15} height={15} />}
        />
      </ToggleButtonGroup>
      <TextField
        value={lineContains[index].value}
        placeholder='Enter text'
        id='standard-basic'
        sx={{
          minWidth: ds.space.mul(0, 141),
          marginBottom: 'var(--ds-space-2)',
          '.MuiInput-root': {
            border: `0.5px solid ${ds.brand[200]}`,
            padding: '0px var(--ds-space-1) !important',
            mt: 'var(--ds-space-2)',
            borderRadius: 'var(--ds-radius-sm)',
          },
          '.MuiInput-root::before': { border: '0' },
          '.MuiInputBase-root-MuiInput-root:hover:not(.Mui-disabled):before': {
            borderBottom: '0px !important',
          },
        }}
        onChange={(e) => {
          callback.addValue(e, index);
        }}
        variant='standard'
      />
    </Grid>
  );
};

export const PrimaryButton = ({ label, handleClick }: any) => {
  return (
    <Button
      value='underlined'
      title={'Submit'}
      onClick={(event) => {
        handleClick(event);
      }}
      sx={{ ...primaryBtnSx }}
    >
      + {label}
    </Button>
  );
};

export const IndexBuilder = ({
  value,
  indicesList,
  callback,
  showPadding = true,
  showMargin = true,
  showBorder = true,
  _sx = {},
  width = 400,
}: any) => {
  return (
    <Grid
      item
      m={showMargin && ds.space[1]}
      p={showPadding && ds.space[2]}
      sx={{
        border: showBorder && `1px solid ${ds.gray[500]}`,
      }}
    >
      <AutoCompleteInput
        label={'Index'}
        options={indicesList}
        value={value}
        onChange={(e) => {
          callback(e);
        }}
        width={width}
        toShowNoOption={false}
        onInputChange={(e) => {
          callback(e);
        }}
      />
    </Grid>
  );
};

export default QueryBuilder;
