import React from 'react';
import { Grid, TextField, Typography, InputAdornment } from '@mui/material';
import Box from '@mui/material/Box';
import { inputSx } from '@data/themes/inputField';
import AutoOptimizeInfoCard from '@components1/autopilot/card/AutoOptimizeInfoCard';
import ButtonTabs from '@components1/common/ButtonTabs';
import { formatMemory } from '@lib/formatter';
import TextWithBorder from '@components1/common/TextWithBorder';
import { DoubleArrowRight } from '@assets';
import buttonConfiguration from '@lib/buttonConfiguration';
import { colors } from 'src/utils/colors';
import SafeIcon from '@components1/common/SafeIcon';

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
            <Box sx={{ marginLeft: '2px' }}>
              <TextWithBorder
                borderWidth='2px'
                borderColor={colors.nudgebeeMain}
                value='CPU'
                sx={{
                  '& p': {
                    fontSize: '14px',
                    color: colors.text.secondary,
                    fontWeight: 600,
                  },
                }}
              />
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
                        <TextField
                          InputProps={{
                            endAdornment: (
                              <InputAdornment position='end' sx={{ '& p': { color: colors.text.lastSync, fontSize: '12px', fontWeight: 400 } }}>
                                CPU
                              </InputAdornment>
                            ),
                          }}
                          sx={{
                            ...inputSx,
                            '& .MuiOutlinedInput-root': {
                              height: '34px',
                            },
                          }}
                          size='small'
                          value={data?.cpu?.request || ''}
                          fullWidth
                          name={'cpuRequest'}
                          type='text'
                          onChange={(e) => {
                            handleInputChange(e.target.value, 'cpu', 'request', containerName);
                          }}
                          disabled={isDisable}
                        />
                      </Grid>
                      <Grid item xs={6}>
                        <Typography sx={{ color: colors.text.tertiary, fontSize: '12px', fontWeight: 400 }}>{'Limit'}</Typography>
                        <TextField
                          InputProps={{
                            endAdornment: (
                              <InputAdornment position='end' sx={{ '& p': { color: colors.text.lastSync, fontSize: '12px', fontWeight: 400 } }}>
                                CPU
                              </InputAdornment>
                            ),
                          }}
                          sx={{
                            ...inputSx,
                            '& .MuiOutlinedInput-root': {
                              height: '34px',
                            },
                          }}
                          size='small'
                          value={data?.cpu?.limit || ''}
                          fullWidth
                          name={'updateCpuLimit'}
                          type='text'
                          onChange={(e) => handleInputChange(e.target.value, 'cpu', 'limit', containerName)}
                          disabled={isDisable || reviewAutoOptimize}
                        />
                      </Grid>
                    </Grid>
                  </Box>
                  <Box sx={{ display: 'flex', gap: '6px', flexDirection: 'column' }}>
                    {buttonConfigs?.buttonsAlgo && (
                      <ButtonTabs
                        key={'algo'}
                        sx={{ height: '30px' }}
                        title='Based on'
                        buttons={buttonConfigs?.buttonsAlgo}
                        callBack={handleSelectedAlgo}
                        color={colors.text.white}
                        fontSize='12px'
                        borderColor={colors.border.primaryLight}
                        background={colors.border.primary}
                        selectedButton={activeButton?.algo}
                        height={'22px'}
                        disabled={reviewAutoOptimize}
                      />
                    )}
                    <ButtonTabs
                      key={'buffer'}
                      sx={{ height: '30px' }}
                      title='Add'
                      buttons={buttonConfigs?.buttonsBuffer}
                      callBack={handleSelectedBuffer}
                      color={colors.text.white}
                      fontSize='12px'
                      borderColor={colors.border.primaryLight}
                      background={colors.border.primary}
                      selectedButton={activeButton?.buffer}
                      height={'22px'}
                      disabled={reviewAutoOptimize}
                    />
                    {handleSelectedCpuLimit && (
                      <ButtonTabs
                        key={'cpuLimit'}
                        sx={{ height: '30px' }}
                        title='Limit'
                        buttons={[
                          { id: 0, label: 'No Limit', value: 'NO_LIMIT' },
                          ...(showKeepPreviousCpuLimit
                            ? [{ id: 1, label: `Keep Previous (${currentData?.cpu?.limit})`, value: 'KEEP_PREVIOUS' }]
                            : []),
                          { id: 2, label: '+5% of Req', value: 'PLUS_5' },
                          { id: 3, label: '+15% of Req', value: 'PLUS_15' },
                        ]}
                        callBack={handleSelectedCpuLimit}
                        color={colors.text.white}
                        fontSize='12px'
                        borderColor={colors.border.primaryLight}
                        background={colors.border.primary}
                        selectedButton={activeButton?.cpuLimit ?? 0}
                        height={'22px'}
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
                      <ButtonTabs
                        key={'algo'}
                        sx={{ height: '30px' }}
                        title='Based on'
                        buttons={buttonConfigs?.buttonsAlgo}
                        callBack={handleSelectedAlgo}
                        color={colors.text.white}
                        fontSize='12px'
                        borderColor={colors.border.primaryLight}
                        background={colors.border.primary}
                        selectedButton={activeButton?.algo}
                        height={'22px'}
                        disabled={reviewAutoOptimize}
                      />
                    )}
                    <ButtonTabs
                      key={'buffer'}
                      sx={{ height: '30px' }}
                      title='Add'
                      buttons={buttonConfigs?.buttonsBuffer}
                      callBack={handleSelectedBuffer}
                      color={colors.text.white}
                      fontSize='12px'
                      borderColor={colors.border.primaryLight}
                      background={colors.border.primary}
                      selectedButton={activeButton?.buffer}
                      height={'22px'}
                      disabled={reviewAutoOptimize}
                    />
                    {handleSelectedCpuLimit && (
                      <ButtonTabs
                        key={'cpuLimit2'}
                        sx={{ height: '30px' }}
                        title='Limit'
                        buttons={[
                          { id: 0, label: 'No Limit', value: 'NO_LIMIT' },
                          ...(showKeepPreviousCpuLimit
                            ? [{ id: 1, label: `Keep Previous (${currentData?.cpu?.limit})`, value: 'KEEP_PREVIOUS' }]
                            : []),
                          { id: 2, label: '+5% of Req', value: 'PLUS_5' },
                          { id: 3, label: '+15% of Req', value: 'PLUS_15' },
                        ]}
                        callBack={handleSelectedCpuLimit}
                        color={colors.text.white}
                        fontSize='12px'
                        borderColor={colors.border.primaryLight}
                        background={colors.border.primary}
                        selectedButton={activeButton?.cpuLimit ?? 0}
                        height={'22px'}
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
            <Box sx={{ marginLeft: '2px' }}>
              <TextWithBorder
                borderWidth='2px'
                borderColor={colors.nudgebeeMain}
                value='Memory'
                sx={{
                  '& p': {
                    fontSize: '14px',
                    color: colors.text.secondary,
                    fontWeight: 600,
                  },
                }}
              />
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
                        <TextField
                          InputProps={{
                            endAdornment: (
                              <InputAdornment position='end' sx={{ '& p': { color: colors.text.lastSync, fontSize: '12px', fontWeight: 400 } }}>
                                MB
                              </InputAdornment>
                            ),
                          }}
                          sx={{
                            ...inputSx,
                            '&.MuiFormControl-root': {
                              borderRadius: '4px',
                            },
                            '& .MuiOutlinedInput-root': {
                              height: '34px',
                            },
                          }}
                          size='small'
                          value={data?.memory?.request || ''}
                          fullWidth
                          name={'memoryRequest'}
                          type='text'
                          onChange={(e) => handleInputChange(e.target.value, 'mem', 'request', containerName)}
                          disabled={isDisable}
                        />
                      </Grid>
                      <Grid item xs={6}>
                        <Typography sx={{ color: colors.text.tertiary, fontSize: '12px', fontWeight: 400 }}>{'Limit'}</Typography>
                        <TextField
                          InputProps={{
                            endAdornment: (
                              <InputAdornment position='end' sx={{ '& p': { color: colors.text.lastSync, fontSize: '12px', fontWeight: 400 } }}>
                                MB
                              </InputAdornment>
                            ),
                          }}
                          sx={{
                            ...inputSx,
                            '& .MuiOutlinedInput-root': {
                              height: '34px',
                            },
                          }}
                          size='small'
                          value={data?.memory?.limit || ''}
                          fullWidth
                          name={'memoryLimit'}
                          type='text'
                          onChange={(e) => handleInputChange(e.target.value, 'mem', 'limit', containerName)}
                          disabled={isDisable}
                        />
                      </Grid>
                    </Grid>
                  </Box>
                  <Box sx={{ display: 'flex', gap: '6px', flexDirection: 'column' }}>
                    <ButtonTabs
                      key={'memoryAlgo'}
                      sx={{ height: '30px' }}
                      title='Based on'
                      buttons={buttonConfigs?.buttonMemoryAlgo}
                      callBack={handleSelectedMemoryAlgo}
                      color={colors.text.white}
                      fontSize='12px'
                      borderColor={colors.border.primaryLight}
                      background={colors.border.primary}
                      selectedButton={activeButton?.memory ?? 0}
                      height={'22px'}
                      disabled={reviewAutoOptimize}
                    />
                    <ButtonTabs
                      key={'memoryBuffer'}
                      sx={{ height: '30px' }}
                      title='Add'
                      buttons={buttonConfigs?.buttonMemoryBuffer}
                      callBack={handleSelectedMemoryBuffer}
                      color={colors.text.white}
                      fontSize='12px'
                      borderColor={colors.border.primaryLight}
                      background={colors.border.primary}
                      selectedButton={activeButton?.memBuffer ?? 0}
                      height={'22px'}
                      disabled={reviewAutoOptimize}
                    />
                    {handleSelectedMemLimit && (
                      <ButtonTabs
                        key={'memLimit'}
                        sx={{ height: '30px' }}
                        title='Limit'
                        buttons={[
                          { id: 0, label: 'Recommended', value: 'RECOMMENDED' },
                          ...(showKeepPreviousMemLimit
                            ? [{ id: 1, label: `Keep Previous (${currentData?.memory?.limit} MB)`, value: 'KEEP_PREVIOUS' }]
                            : []),
                          { id: 2, label: '+5% of Req', value: 'PLUS_5' },
                          { id: 3, label: '+15% of Req', value: 'PLUS_15' },
                        ]}
                        callBack={handleSelectedMemLimit}
                        color={colors.text.white}
                        fontSize='12px'
                        borderColor={colors.border.primaryLight}
                        background={colors.border.primary}
                        selectedButton={activeButton?.memLimit ?? 0}
                        height={'22px'}
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
                    <ButtonTabs
                      key={'memoryAlgo2'}
                      sx={{ height: '30px' }}
                      title='Based on'
                      buttons={buttonConfigs?.buttonMemoryAlgo}
                      callBack={handleSelectedMemoryAlgo}
                      color={colors.text.white}
                      fontSize='12px'
                      borderColor={colors.border.primaryLight}
                      background={colors.border.primary}
                      selectedButton={activeButton?.memory ?? 0}
                      height={'22px'}
                      disabled={reviewAutoOptimize}
                    />
                    <ButtonTabs
                      key={'memoryBuffer2'}
                      sx={{ height: '30px' }}
                      title='Add'
                      buttons={buttonConfigs?.buttonMemoryBuffer}
                      callBack={handleSelectedMemoryBuffer}
                      color={colors.text.white}
                      fontSize='12px'
                      borderColor={colors.border.primaryLight}
                      background={colors.border.primary}
                      selectedButton={activeButton?.memBuffer ?? 0}
                      height={'22px'}
                      disabled={reviewAutoOptimize}
                    />
                    {handleSelectedMemLimit && (
                      <ButtonTabs
                        key={'memLimit2'}
                        sx={{ height: '30px' }}
                        title='Limit'
                        buttons={[
                          { id: 0, label: 'Recommended', value: 'RECOMMENDED' },
                          ...(showKeepPreviousMemLimit
                            ? [{ id: 1, label: `Keep Previous (${currentData?.memory?.limit} MB)`, value: 'KEEP_PREVIOUS' }]
                            : []),
                          { id: 2, label: '+5% of Req', value: 'PLUS_5' },
                          { id: 3, label: '+15% of Req', value: 'PLUS_15' },
                        ]}
                        callBack={handleSelectedMemLimit}
                        color={colors.text.white}
                        fontSize='12px'
                        borderColor={colors.border.primaryLight}
                        background={colors.border.primary}
                        selectedButton={activeButton?.memLimit ?? 0}
                        height={'22px'}
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
