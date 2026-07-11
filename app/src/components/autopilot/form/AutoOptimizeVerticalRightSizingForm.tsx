import React from 'react';
import { Grid, Typography } from '@mui/material';
import Box from '@mui/material/Box';
import { Input } from '@ui/Input';
import { ToggleGroup } from '@ui/ToggleGroup';
import AutoOptimizeInfoCard from '@components/autopilot/card/AutoOptimizeInfoCard';
import { formatMemory } from '@lib/formatter';
import { DoubleArrowRight } from '@assets';
import buttonConfiguration from '@lib/buttonConfiguration';
import SafeIcon from '@shared/icons/SafeIcon';

interface ToggleOption {
  id: string | number;
  label: string;
  value?: any;
}

interface LabeledToggleGroupProps {
  title: string;
  options: ToggleOption[];
  selected?: string | number;
  onChange: (id: string | number, value: any) => void;
  disabled?: boolean;
}

function LabeledToggleGroup({ title, options, selected, onChange, disabled }: LabeledToggleGroupProps) {
  const value = String(selected ?? options[0]?.id ?? '');
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
      <Typography
        sx={{
          color: 'var(--ds-brand-500)',
          fontSize: 'var(--ds-text-caption)',
          fontWeight: 'var(--ds-font-weight-regular)',
          minWidth: 'calc(var(--ds-space-0) * 22)',
        }}
      >
        {title}
      </Typography>
      <ToggleGroup
        selection='single'
        size='sm'
        ariaLabel={title}
        value={value}
        onChange={(next) => {
          const opt = options.find((o) => String(o.id) === next);
          if (opt) onChange(opt.id, opt.value);
        }}
        options={options.map((o) => ({ value: String(o.id), label: o.label, disabled }))}
      />
    </Box>
  );
}

function capitalizeFirstLetter(str: string): string {
  return str.charAt(0).toUpperCase() + str.slice(1);
}

interface InfoCardData {
  request?: string;
  limit?: string;
  [key: string]: any;
}

interface InfoCardProps {
  data: InfoCardData;
  type?: string;
}

const VerticalAutopPilotForm = ({
  buttonConfigs = buttonConfiguration.buttonConfigs,
  handleSelectedAlgo,
  handleSelectedBuffer,
  handleSelectedMemoryBuffer,
  handleSelectedMemoryAlgo,
  handleSelectedCpuLimit,
  handleSelectedMemLimit,
  data = {},
  currentData,
  children,
  activeButton,
  additionalInfoCPUAndMem,
  handleInputChange,
  isDisable = false,
  reviewAutoOptimize,
  containerName = '',
  showKeepPreviousCpuLimit = false,
  showKeepPreviousMemLimit = false,
}: VerticalAutopPilotFormProps) => {
  const InfoCard = ({ data, type }: InfoCardProps) => {
    return (
      <Box>
        <Box sx={{ display: 'flex', width: 'calc(var(--ds-space-0) * 110)', justifyContent: 'space-between' }}>
          {Object.keys(data).map((key, index) => (
            <Box key={index} sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
              <Typography sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }}>
                {capitalizeFirstLetter(key)}
              </Typography>
              <Typography
                sx={{
                  color: 'var(--ds-gray-400)',
                  fontSize: 'var(--ds-text-body-lg)',
                  fontWeight: 'var(--ds-font-weight-medium)',
                  mb: 'var(--ds-space-4)',
                }}
              >
                {data[key]}{' '}
                <span style={{ color: 'var(--ds-brand-300)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }}>
                  {' '}
                  {type === 'memory' ? 'MB' : 'CPU'}{' '}
                </span>
              </Typography>
            </Box>
          ))}
        </Box>
        <Box sx={{ display: 'flex', flexDirection: 'column' }}>
          {type !== 'memory' && additionalInfoCPUAndMem?.cpuInfo?.p99 ? (
            <Typography sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}>
              <ul style={{ margin: '0px', paddingLeft: 'calc(var(--ds-space-1) * 5)' }}>
                <li>99% of the time, the CPU usage was below {parseFloat(additionalInfoCPUAndMem?.cpuInfo?.p99).toFixed(4)}</li>
              </ul>
            </Typography>
          ) : (
            additionalInfoCPUAndMem?.memInfo?.req && (
              <Typography sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}>
                <ul style={{ margin: '0px', paddingLeft: 'calc(var(--ds-space-1) * 5)' }}>
                  <li>99% of the time, the Memory usage was below {formatMemory(additionalInfoCPUAndMem?.memInfo?.req)}</li>
                </ul>
              </Typography>
            )
          )}
          {type !== 'memory' ? (
            <Typography sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)' }}>
              <ul style={{ margin: '0px', paddingLeft: 'calc(var(--ds-space-1) * 5)' }}>
                <li>
                  {activeButton?.cpuLimit === 1
                    ? `CPU Limit will be set to previous value (${data?.limit})`
                    : 'CPU Limit should be set to none to allow temporary spike'}
                </li>
              </ul>
            </Typography>
          ) : (
            <></>
          )}
        </Box>
      </Box>
    );
  };

  return (
    <>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
        {data?.cpu && (
          <Box sx={{ gap: 'var(--ds-space-4)', display: 'flex', flexDirection: 'column' }}>
            <Box sx={{ marginLeft: 'var(--ds-space-0)', borderLeft: '2px solid var(--ds-yellow-500)', padding: '0px calc(var(--ds-space-0) * 5)' }}>
              <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-brand-500)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                CPU
              </Typography>
            </Box>

            {currentData?.cpu ? (
              <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: 'calc(var(--ds-space-0) * 5)' }}>
                <Box>
                  <AutoOptimizeInfoCard title='Current' border={'1px solid var(--ds-gray-200)'}>
                    <InfoCard data={currentData.cpu} />
                  </AutoOptimizeInfoCard>
                </Box>
                <Box
                  sx={{
                    width: '30px',
                    '& img': {
                      my: 'var(--ds-space-2)',
                    },
                  }}
                >
                  <SafeIcon src={DoubleArrowRight} alt='arrow right' style={{ opacity: 0.5 }} />
                  <SafeIcon src={DoubleArrowRight} alt='arrow right' />
                  <SafeIcon src={DoubleArrowRight} alt='arrow right' style={{ opacity: 0.5 }} />
                </Box>
                <AutoOptimizeInfoCard
                  shadow={
                    '0px var(--ds-space-0) 7px 0px color-mix(in srgb, var(--ds-green-500) 6%, transparent), 0px var(--ds-space-1) calc(var(--ds-space-0) * 3) -1px color-mix(in srgb, var(--ds-green-500) 12%, transparent)'
                  }
                  title='Recommended'
                  button={<></>}
                  border={'0.5px solid var(--ds-green-500)'}
                  height='auto'
                >
                  <Box display='flex' flexDirection='column' justifyContent='space-between' alignItems='left'>
                    <Grid container spacing={2}>
                      <Grid item xs={6}>
                        <Typography
                          sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }}
                        >
                          {'Request'}
                        </Typography>
                        <Input
                          suffix='CPU'
                          size='sm'
                          value={data?.cpu?.request || ''}
                          name='cpuRequest'
                          onChange={(value) => handleInputChange(value, 'cpu', 'request', containerName)}
                          disabled={isDisable}
                        />
                      </Grid>
                      <Grid item xs={6}>
                        <Typography
                          sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }}
                        >
                          {'Limit'}
                        </Typography>
                        <Input
                          suffix='CPU'
                          size='sm'
                          value={data?.cpu?.limit || ''}
                          name='updateCpuLimit'
                          onChange={(value) => handleInputChange(value, 'cpu', 'limit', containerName)}
                          disabled={isDisable || reviewAutoOptimize}
                        />
                      </Grid>
                    </Grid>
                  </Box>
                  <Box sx={{ display: 'flex', gap: 'calc(var(--ds-space-0) * 3)', flexDirection: 'column' }}>
                    {buttonConfigs?.buttonsAlgo && (
                      <LabeledToggleGroup
                        title='Based on'
                        options={buttonConfigs.buttonsAlgo}
                        selected={activeButton?.algo}
                        onChange={handleSelectedAlgo}
                        disabled={reviewAutoOptimize}
                      />
                    )}
                    <LabeledToggleGroup
                      title='Add'
                      options={buttonConfigs?.buttonsBuffer || []}
                      selected={activeButton?.buffer}
                      onChange={handleSelectedBuffer}
                      disabled={reviewAutoOptimize}
                    />
                    {handleSelectedCpuLimit && (
                      <LabeledToggleGroup
                        title='Limit'
                        options={[
                          { id: 0, label: 'No Limit', value: 'NO_LIMIT' },
                          ...(showKeepPreviousCpuLimit
                            ? [{ id: 1, label: `Keep Previous (${currentData?.cpu?.limit})`, value: 'KEEP_PREVIOUS' }]
                            : []),
                          { id: 2, label: '+5% of Req', value: 'PLUS_5' },
                          { id: 3, label: '+15% of Req', value: 'PLUS_15' },
                        ]}
                        selected={activeButton?.cpuLimit ?? 0}
                        onChange={handleSelectedCpuLimit}
                        disabled={reviewAutoOptimize}
                      />
                    )}
                  </Box>
                </AutoOptimizeInfoCard>
              </Box>
            ) : (
              <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: 'calc(var(--ds-space-0) * 5)' }}>
                <AutoOptimizeInfoCard
                  shadow={
                    '0px var(--ds-space-0) 7px 0px color-mix(in srgb, var(--ds-green-500) 6%, transparent), 0px var(--ds-space-1) calc(var(--ds-space-0) * 3) -1px color-mix(in srgb, var(--ds-green-500) 12%, transparent)'
                  }
                  title='Recommended'
                  button={<></>}
                  border={'0.5px solid var(--ds-green-500)'}
                  height='120px'
                >
                  <Box sx={{ display: 'flex', gap: 'calc(var(--ds-space-0) * 3)', flexDirection: 'column' }}>
                    {buttonConfigs?.buttonsAlgo && (
                      <LabeledToggleGroup
                        title='Based on'
                        options={buttonConfigs.buttonsAlgo}
                        selected={activeButton?.algo}
                        onChange={handleSelectedAlgo}
                        disabled={reviewAutoOptimize}
                      />
                    )}
                    <LabeledToggleGroup
                      title='Add'
                      options={buttonConfigs?.buttonsBuffer || []}
                      selected={activeButton?.buffer}
                      onChange={handleSelectedBuffer}
                      disabled={reviewAutoOptimize}
                    />
                    {handleSelectedCpuLimit && (
                      <LabeledToggleGroup
                        title='Limit'
                        options={[
                          { id: 0, label: 'No Limit', value: 'NO_LIMIT' },
                          ...(showKeepPreviousCpuLimit
                            ? [{ id: 1, label: `Keep Previous (${currentData?.cpu?.limit})`, value: 'KEEP_PREVIOUS' }]
                            : []),
                          { id: 2, label: '+5% of Req', value: 'PLUS_5' },
                          { id: 3, label: '+15% of Req', value: 'PLUS_15' },
                        ]}
                        selected={activeButton?.cpuLimit ?? 0}
                        onChange={handleSelectedCpuLimit}
                        disabled={reviewAutoOptimize}
                      />
                    )}
                  </Box>
                </AutoOptimizeInfoCard>
              </Box>
            )}
          </Box>
        )}

        {data?.memory && (
          <Box sx={{ gap: 'var(--ds-space-4)', display: 'flex', flexDirection: 'column' }}>
            <Box sx={{ marginLeft: 'var(--ds-space-0)', borderLeft: '2px solid var(--ds-yellow-500)', padding: '0px calc(var(--ds-space-0) * 5)' }}>
              <Typography sx={{ fontSize: 'var(--ds-text-body-lg)', color: 'var(--ds-brand-500)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                Memory
              </Typography>
            </Box>
            {currentData?.memory ? (
              <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: 'calc(var(--ds-space-0) * 5)' }}>
                <Box>
                  <AutoOptimizeInfoCard title='Current' border={'1px solid var(--ds-gray-200)'}>
                    <InfoCard data={currentData.memory} type='memory' />
                  </AutoOptimizeInfoCard>
                </Box>
                <Box
                  sx={{
                    width: '30px',
                    '& img': {
                      my: 'var(--ds-space-2)',
                    },
                  }}
                >
                  <SafeIcon src={DoubleArrowRight} alt='arrow right' style={{ opacity: 0.5 }} />
                  <SafeIcon src={DoubleArrowRight} alt='arrow right' />
                  <SafeIcon src={DoubleArrowRight} alt='arrow right' style={{ opacity: 0.5 }} />
                </Box>
                <AutoOptimizeInfoCard
                  shadow={
                    '0px var(--ds-space-0) 7px 0px color-mix(in srgb, var(--ds-green-500) 6%, transparent), 0px var(--ds-space-1) calc(var(--ds-space-0) * 3) -1px color-mix(in srgb, var(--ds-green-500) 12%, transparent)'
                  }
                  title='Recommended'
                  button={<></>}
                  border={'0.5px solid var(--ds-green-500)'}
                  height='auto'
                >
                  <Box display='flex' flexDirection='column' justifyContent='space-between' alignItems='left'>
                    <Grid container spacing={2}>
                      <Grid item xs={6}>
                        <Typography
                          sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }}
                        >
                          {'Request'}
                        </Typography>
                        <Input
                          suffix='MB'
                          size='sm'
                          value={data?.memory?.request || ''}
                          name='memoryRequest'
                          onChange={(value) => handleInputChange(value, 'mem', 'request', containerName)}
                          disabled={isDisable}
                        />
                      </Grid>
                      <Grid item xs={6}>
                        <Typography
                          sx={{ color: 'var(--ds-gray-600)', fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)' }}
                        >
                          {'Limit'}
                        </Typography>
                        <Input
                          suffix='MB'
                          size='sm'
                          value={data?.memory?.limit || ''}
                          name='memoryLimit'
                          onChange={(value) => handleInputChange(value, 'mem', 'limit', containerName)}
                          disabled={isDisable}
                        />
                      </Grid>
                    </Grid>
                  </Box>
                  <Box sx={{ display: 'flex', gap: 'calc(var(--ds-space-0) * 3)', flexDirection: 'column' }}>
                    <LabeledToggleGroup
                      title='Based on'
                      options={buttonConfigs?.buttonMemoryAlgo || []}
                      selected={activeButton?.memory ?? 0}
                      onChange={handleSelectedMemoryAlgo}
                      disabled={reviewAutoOptimize}
                    />
                    <LabeledToggleGroup
                      title='Add'
                      options={buttonConfigs?.buttonMemoryBuffer || []}
                      selected={activeButton?.memBuffer ?? 0}
                      onChange={handleSelectedMemoryBuffer}
                      disabled={reviewAutoOptimize}
                    />
                    {handleSelectedMemLimit && (
                      <LabeledToggleGroup
                        title='Limit'
                        options={[
                          { id: 0, label: 'Recommended', value: 'RECOMMENDED' },
                          ...(showKeepPreviousMemLimit
                            ? [{ id: 1, label: `Keep Previous (${currentData?.memory?.limit} MB)`, value: 'KEEP_PREVIOUS' }]
                            : []),
                          { id: 2, label: '+5% of Req', value: 'PLUS_5' },
                          { id: 3, label: '+15% of Req', value: 'PLUS_15' },
                        ]}
                        selected={activeButton?.memLimit ?? 0}
                        onChange={handleSelectedMemLimit}
                        disabled={reviewAutoOptimize}
                      />
                    )}
                  </Box>
                </AutoOptimizeInfoCard>
              </Box>
            ) : (
              <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: 'calc(var(--ds-space-0) * 5)' }}>
                <AutoOptimizeInfoCard
                  shadow={
                    '0px var(--ds-space-0) 7px 0px color-mix(in srgb, var(--ds-green-500) 6%, transparent), 0px var(--ds-space-1) calc(var(--ds-space-0) * 3) -1px color-mix(in srgb, var(--ds-green-500) 12%, transparent)'
                  }
                  title='Recommended'
                  button={<></>}
                  border={'0.5px solid var(--ds-green-500)'}
                  height='120px'
                >
                  <Box sx={{ display: 'flex', gap: 'calc(var(--ds-space-0) * 3)', flexDirection: 'column' }}>
                    <LabeledToggleGroup
                      title='Based on'
                      options={buttonConfigs?.buttonMemoryAlgo || []}
                      selected={activeButton?.memory ?? 0}
                      onChange={handleSelectedMemoryAlgo}
                      disabled={reviewAutoOptimize}
                    />
                    <LabeledToggleGroup
                      title='Add'
                      options={buttonConfigs?.buttonMemoryBuffer || []}
                      selected={activeButton?.memBuffer ?? 0}
                      onChange={handleSelectedMemoryBuffer}
                      disabled={reviewAutoOptimize}
                    />
                    {handleSelectedMemLimit && (
                      <LabeledToggleGroup
                        title='Limit'
                        options={[
                          { id: 0, label: 'Recommended', value: 'RECOMMENDED' },
                          ...(showKeepPreviousMemLimit
                            ? [{ id: 1, label: `Keep Previous (${currentData?.memory?.limit} MB)`, value: 'KEEP_PREVIOUS' }]
                            : []),
                          { id: 2, label: '+5% of Req', value: 'PLUS_5' },
                          { id: 3, label: '+15% of Req', value: 'PLUS_15' },
                        ]}
                        selected={activeButton?.memLimit ?? 0}
                        onChange={handleSelectedMemLimit}
                        disabled={reviewAutoOptimize}
                      />
                    )}
                  </Box>
                </AutoOptimizeInfoCard>
              </Box>
            )}
          </Box>
        )}
      </Box>
      {children}
    </>
  );
};

export default VerticalAutopPilotForm;
