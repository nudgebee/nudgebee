import React from 'react';
import { Grid, Typography } from '@mui/material';
import Box from '@mui/material/Box';
import { Input } from '@components1/ds/Input';
import { ToggleGroup } from '@components1/ds/ToggleGroup';
import AutoOptimizeInfoCard from '@components1/autopilot/card/AutoOptimizeInfoCard';
import { formatMemory } from '@lib/formatter';
import { DoubleArrowRight } from '@assets';
import buttonConfiguration from '@lib/buttonConfiguration';
import { colors } from 'src/utils/colors';
import SafeIcon from '@components1/common/SafeIcon';

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
    <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
      <Typography sx={{ color: colors.text.secondary, fontSize: '10px', fontWeight: 400, minWidth: '43px' }}>{title}</Typography>
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
        <Box sx={{ display: 'flex', width: '219px', justifyContent: 'space-between' }}>
          {Object.keys(data).map((key, index) => (
            <Box key={index} sx={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
              <Typography sx={{ color: colors.text.tertiary, fontSize: '12px', fontWeight: 400 }}>{capitalizeFirstLetter(key)}</Typography>
              <Typography sx={{ color: colors.text.secondaryDark, fontSize: '14px', fontWeight: 500, mb: '15px' }}>
                {data[key]}{' '}
                <span style={{ color: colors.text.lastSync, fontSize: '12px', fontWeight: 400 }}> {type === 'memory' ? 'MB' : 'CPU'} </span>
              </Typography>
            </Box>
          ))}
        </Box>
        <Box sx={{ display: 'flex', flexDirection: 'column' }}>
          {type !== 'memory' && additionalInfoCPUAndMem?.cpuInfo?.p99 ? (
            <Typography sx={{ color: colors.text.tertiary, fontSize: '12px', fontWeight: 500 }}>
              <ul style={{ margin: '0px', paddingLeft: '20px' }}>
                <li>99% of the time, the CPU usage was below {parseFloat(additionalInfoCPUAndMem?.cpuInfo?.p99).toFixed(4)}</li>
              </ul>
            </Typography>
          ) : (
            additionalInfoCPUAndMem?.memInfo?.req && (
              <Typography sx={{ color: colors.text.tertiary, fontSize: '12px', fontWeight: 500 }}>
                <ul style={{ margin: '0px', paddingLeft: '20px' }}>
                  <li>99% of the time, the Memory usage was below {formatMemory(additionalInfoCPUAndMem?.memInfo?.req)}</li>
                </ul>
              </Typography>
            )
          )}
          {type !== 'memory' ? (
            <Typography sx={{ color: colors.text.tertiary, fontSize: '12px', fontWeight: 500 }}>
              <ul style={{ margin: '0px', paddingLeft: '20px' }}>
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
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>
        {data?.cpu && (
          <Box sx={{ gap: '16px', display: 'flex', flexDirection: 'column' }}>
            <Box sx={{ marginLeft: '2px', borderLeft: `2px solid ${colors.nudgebeeMain}`, padding: '0px 10px' }}>
              <Typography sx={{ fontSize: '14px', color: colors.text.secondary, fontWeight: 600 }}>CPU</Typography>
            </Box>

            {currentData?.cpu ? (
              <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: '10px' }}>
                <Box>
                  <AutoOptimizeInfoCard title='Current' border={`1px solid ${colors.border.vertical}`}>
                    <InfoCard data={currentData.cpu} />
                  </AutoOptimizeInfoCard>
                </Box>
                <Box
                  sx={{
                    width: '30px',
                    '& img': {
                      my: '8px',
                    },
                  }}
                >
                  <SafeIcon src={DoubleArrowRight} alt='arrow right' style={{ opacity: 0.5 }} />
                  <SafeIcon src={DoubleArrowRight} alt='arrow right' />
                  <SafeIcon src={DoubleArrowRight} alt='arrow right' style={{ opacity: 0.5 }} />
                </Box>
                <AutoOptimizeInfoCard
                  shadow={' 0px 2px 7px 0px #22C55E0F, 0px 4px 6px -1px #22C55E1F'}
                  title='Recommended'
                  button={<></>}
                  border={`0.5px solid ${colors.border.cpuRecommendation}`}
                  height='auto'
                >
                  <Box display='flex' flexDirection='column' justifyContent='space-between' alignItems='left'>
                    <Grid container spacing={2}>
                      <Grid item xs={6}>
                        <Typography sx={{ color: colors.text.tertiary, fontSize: '12px', fontWeight: 400 }}>{'Request'}</Typography>
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
                        <Typography sx={{ color: colors.text.tertiary, fontSize: '12px', fontWeight: 400 }}>{'Limit'}</Typography>
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
                  <Box sx={{ display: 'flex', gap: '6px', flexDirection: 'column' }}>
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
              <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: '10px' }}>
                <AutoOptimizeInfoCard
                  shadow={' 0px 2px 7px 0px #22C55E0F, 0px 4px 6px -1px #22C55E1F'}
                  title='Recommended'
                  button={<></>}
                  border={`0.5px solid ${colors.border.cpuRecommendation}`}
                  height='120px'
                >
                  <Box sx={{ display: 'flex', gap: '6px', flexDirection: 'column' }}>
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
          <Box sx={{ gap: '16px', display: 'flex', flexDirection: 'column' }}>
            <Box sx={{ marginLeft: '2px', borderLeft: `2px solid ${colors.nudgebeeMain}`, padding: '0px 10px' }}>
              <Typography sx={{ fontSize: '14px', color: colors.text.secondary, fontWeight: 600 }}>Memory</Typography>
            </Box>
            {currentData?.memory ? (
              <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: '10px' }}>
                <Box>
                  <AutoOptimizeInfoCard title='Current' border={`1px solid ${colors.border.vertical}`}>
                    <InfoCard data={currentData.memory} type='memory' />
                  </AutoOptimizeInfoCard>
                </Box>
                <Box
                  sx={{
                    width: '30px',
                    '& img': {
                      my: '8px',
                    },
                  }}
                >
                  <SafeIcon src={DoubleArrowRight} alt='arrow right' style={{ opacity: 0.5 }} />
                  <SafeIcon src={DoubleArrowRight} alt='arrow right' />
                  <SafeIcon src={DoubleArrowRight} alt='arrow right' style={{ opacity: 0.5 }} />
                </Box>
                <AutoOptimizeInfoCard
                  shadow={' 0px 2px 7px 0px #22C55E0F, 0px 4px 6px -1px #22C55E1F'}
                  title='Recommended'
                  button={<></>}
                  border={`0.5px solid ${colors.border.cpuRecommendation}`}
                  height='auto'
                >
                  <Box display='flex' flexDirection='column' justifyContent='space-between' alignItems='left'>
                    <Grid container spacing={2}>
                      <Grid item xs={6}>
                        <Typography sx={{ color: colors.text.tertiary, fontSize: '12px', fontWeight: 400 }}>{'Request'}</Typography>
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
                        <Typography sx={{ color: colors.text.tertiary, fontSize: '12px', fontWeight: 400 }}>{'Limit'}</Typography>
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
                  <Box sx={{ display: 'flex', gap: '6px', flexDirection: 'column' }}>
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
              <Box sx={{ display: 'flex', flexDirection: 'row', alignItems: 'center', gap: '10px' }}>
                <AutoOptimizeInfoCard
                  shadow={' 0px 2px 7px 0px #22C55E0F, 0px 4px 6px -1px #22C55E1F'}
                  title='Recommended'
                  button={<></>}
                  border={`0.5px solid ${colors.border.cpuRecommendation}`}
                  height='120px'
                >
                  <Box sx={{ display: 'flex', gap: '6px', flexDirection: 'column' }}>
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
