import React, { useEffect, useState } from 'react';
import CustomDropdown from '@components1/common/CustomDropdown';
import { createFilterOptions } from '@mui/material/Autocomplete';
import { Box, Button, Grid, TextField, ToggleButton, ToggleButtonGroup, autocompleteClasses } from '@mui/material';
import AutoCompleteInput from '@components1/common/inputs/AutoCompleteInput';
import { colors } from 'src/utils/colors';
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
  p: '0 5px 0 !important',
  width: '-webkit-fill-available',
  minHeight: '31px',
  border: '0px !important',
  borderRadius: '2px',
  '&:hover': {
    boxShadow: 'unset',
  },
  '& .MuiFormControl-root': {
    m: '3px 0 0px 0!important',
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
  color: 'black',
  fontSize: '0.875rem',
  fontWeight: 500,
  width: '100%',
  height: '33px',
  margin: '0 5px 5px 0',
};
const toggleBtnSx = {
  m: '0 !important',
  p: 0,
  minWidth: 160,
};

const toggleIconBtnSx = {
  ml: '30 !important',
  color: colors.text.delete,
  backgroundColor: colors.background.toggleIconBtn,
  border: `1px solid ${colors.text.delete}`,
  '&:hover': {
    boxShadow: 'unset',
    backgroundColor: colors.background.toggleIconBtnHover,
  },
};

const primaryBtnSx = {
  top: '5px',
  height: '32px',
  m: '0 5px 0 0 !important',
  color: 'white',
  backgroundColor: colors.background.toggleIconBtn,
  '&:hover': {
    boxShadow: 'unset',
    backgroundColor: colors.background.toggleIconBtnHover,
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
          minWidth='120px'
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
      <ToggleButton value='color' aria-label='color' title={operator} sx={{ ...toggleBtnSx, minWidth: 70 }}>
        <CustomDropdown
          options={chipOperators}
          minWidth='70px'
          label=''
          onChange={(e) => {
            callback.addOperator(e);
          }}
          value={operator ?? defaultOperator}
          inputVariant='standard'
          customStyle={{ ...dropDownSx, width: '50px !important' }}
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
              pl: '10px',
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
            minWidth='120px'
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
                  minWidth: '120px',
                  width: 'fit-content',
                },
              },
            }}
          />
        )}
      </ToggleButton>
      <Box sx={{ marginLeft: '4px' }}>
        <CustomButton
          className='custom-delete-btn'
          variant='tertiary'
          onClick={() => {
            callback.removeLabelFilter(indexId);
          }}
          disabled={removeFilter}
          sx={{
            ...toggleIconBtnSx,
            border: '1px solid #FCA5A5 !important',
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
        ...(showBorder && { border: '1px solid grey' }),
        ...(showMargin && { m: 0.5 }),
        ...(showPadding && { p: 1 }),
      }}
    >
      <ToggleButtonGroup size='small' aria-label='text formatting' sx={{ ...toggleBtnGrpSx, gap: '4px' }}>
        <ToggleButton value='underlined' title={'label'} aria-label='color' sx={{ ...toggleBtnSx, width: '100%' }}>
          <CustomDropdown
            options={getLineOperators(operatorDescriptors)}
            minWidth='70px'
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
            border: '1px solid #FCA5A5 !important',
          }}
          startIcon={<SafeIcon src={DeleteIconRed} alt='delete' width={15} height={15} />}
        />
      </ToggleButtonGroup>
      <TextField
        value={lineContains[index].value}
        placeholder='Enter text'
        id='standard-basic'
        sx={{
          minWidth: '282px',
          marginBottom: '8px',
          '.MuiInput-root': { border: `0.5px solid ${colors.border.input}`, padding: '0px 5px !important', mt: '8px', borderRadius: '4px' },
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
      m={showMargin && 0.5}
      p={showPadding && 1}
      sx={{
        border: showBorder && '1px solid grey',
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
