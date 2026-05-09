import { Box, Divider, Grid, Typography } from '@mui/material';
import TextWithBorder from '@components1/common/TextWithBorder';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import AccordionSmall from '@components1/common/AccordionSmall';
import ContainerDetails from './ContainerDetails';
import VolumeDetails from './VolumeDetails';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import { colors } from 'src/utils/colors';
import PropTypes from 'prop-types';
import Datetime from '@components1/common/format/Datetime';

import ReactLink from 'next/link';
import CustomTable from '@components1/common/tables/CustomTable2';
import { snakeToTitleCase } from 'src/utils/common';

const PodDetailsBox = ({ pod, wordBreak, accountId }) => {
  const mapLabels = (label) => {
    const labelArray = [];

    for (let item in label) {
      let name = item + '=' + label[item];
      labelArray.push(
        <CustomLabels
          textTransform='none'
          height='auto'
          margin='0px'
          wordBreak={wordBreak}
          displayTooltip
          key={item.id}
          text={name}
          variant={'grey'}
        />
      );
    }
    return labelArray;
  };

  const MapContainerHeader = ({ containers, _children }) => {
    const containersArray = [];
    if (containers && containers.length > 0) {
      containers?.forEach((item) => {
        containersArray.push(
          <Box key={item.name} sx={{ marginBottom: '10px' }}>
            <AccordionSmall header={item.name}>
              <ContainerDetails containerItem={item} />
            </AccordionSmall>
          </Box>
        );
      });
    }
    return containersArray;
  };
  const MapVolumeHeader = ({ volumes, _children }) => {
    const volumesArray = [];
    volumes?.forEach((item) => {
      volumesArray.push(
        <Box key={item.name} sx={{ marginBottom: '10px' }}>
          <AccordionSmall header={item.name}>
            <VolumeDetails volumeItem={item} />
          </AccordionSmall>
        </Box>
      );
    });
    return volumesArray;
  };

  const TOLERATION_HEADERS = [
    'Key',
    { name: 'Operator', sortEnabled: true },
    { name: 'Value', sortEnabled: true },
    { name: 'Effects', sortEnabled: true },
    { name: 'Seconds', sortEnabled: true },
  ];

  const tolerationRows = pod?.meta?.config?.toleration?.map((item) => {
    return [
      { text: item.key },
      { text: item.operator },
      { text: item.value ? item.value : `-` },
      { text: item.effect },
      { text: item.toleration_seconds },
    ];
  });

  const getPodStatus = (data) => {
    let status = '';
    let color = '';
    if (data) {
      if (typeof data === 'string') {
        color = data === 'Running' ? 'green' : 'red';
        status = data;
      } else if (typeof data === 'object' && data.succeeded !== undefined) {
        color = data.succeeded === 1 ? 'green' : 'red';
        status = data.succeeded === 1 ? 'Completed' : 'Not-Completed';
      } else {
        color = 'yellow';
        status = 'Unknown';
      }
    }

    return { color, status };
  };

  const getStatusInfoConditions = () => {
    let headers = ['Time', 'Status', 'Type', 'Reason', 'Message'];
    let data = pod?.meta?.status_info?.conditions?.map((item) => {
      return [
        {
          component: <Datetime value={item.lastTransitionTime ?? item.last_transition_time} />,
        },
        {
          text: item.status,
        },
        {
          text: item.type,
        },
        {
          text: item.reason ?? '-',
        },
        {
          text: item.message ?? '-',
        },
      ];
    });

    return (
      <>
        {data?.length > 0 && (
          <Box sx={{ padding: '12px 0px 0px 10px' }}>
            <Box key={'status_conditions'} sx={{ marginBottom: '10px' }}>
              <AccordionSmall header={'Conditions'}>
                <KubernetesTable2 headers={headers} data={data} totalRows={data?.length} rowsPerPage={data?.length} />
              </AccordionSmall>
            </Box>
          </Box>
        )}
      </>
    );
  };

  const getStatusInfoContainerStatus = () => {
    let headers = [
      'Name',
      'State',
      'State Reason',
      'Message',
      { name: 'State Started', width: '5%' },
      'State Finished',
      'Restart Count',
      'Ready',
      'Started',
      'Last State',
    ];
    const containerStatus = pod?.meta?.status_info?.container_statuses || pod?.meta?.status_info?.containerStatuses;
    let data = containerStatus
      ?.map((item) => {
        let states = Object.keys(item.state ?? {}) || [];
        states = states.filter((f) => item.state[f]);
        const lastStateKey = Object.keys(item?.last_state || {})?.find((key) => item.last_state[key] !== null) || {};
        const rawLastState = item?.last_state?.[lastStateKey] || {};
        const lastState = Object.fromEntries(Object.entries(rawLastState)?.filter(([_, value]) => value !== null)) || {};

        return states.map((state) => {
          return [
            {
              text: item.name,
            },
            {
              text: state,
            },
            {
              text: item.state[state]?.reason ?? '-',
            },
            {
              text: item.state[state]?.message ?? '-',
            },
            {
              component: <Datetime value={item.state[state]?.startedAt ?? item.state[state]?.started_at} />,
            },
            {
              component: <Datetime value={item.state[state]?.finishedAt ?? item.state[state]?.finished_at} />,
            },
            {
              text: item.restartCount ?? item.restart_count ?? '-',
            },
            {
              text: item.ready ? 'True' : 'False',
            },
            {
              text: item.started ? 'True' : 'False',
            },
            {
              component: (
                <div>
                  {Object.keys(lastState).length > 0 ? (
                    <Box
                      sx={{
                        '& ul': {
                          listStylePosition: 'outside',
                          pl: '0px',
                        },
                      }}
                    >
                      <h3>Last State was &quot;{lastStateKey}&quot;:</h3>
                      <ul>
                        {Object.entries(lastState).map(([key, value]) => (
                          <li key={key}>
                            <Box component='span' sx={{ display: 'flex', alignItems: 'center', gap: 0.5, lineBreak: 'loose' }}>
                              <strong style={{ display: 'flex', alignItems: 'center', minWidth: '100px' }}>{snakeToTitleCase(key)}:</strong>
                              <Box sx={{ minWidth: '200px', maxWidth: '250px', wordBreak: 'break-all' }}>
                                {key === 'started_at' || key === 'finished_at' ? (
                                  <Datetime value={value} />
                                ) : value !== null ? (
                                  value.toString()
                                ) : (
                                  'null'
                                )}
                              </Box>
                            </Box>
                          </li>
                        ))}
                      </ul>
                    </Box>
                  ) : (
                    <p>No status data available.</p>
                  )}
                </div>
              ),
            },
          ];
        });
      })
      .flat();
    return (
      <>
        {data?.length > 0 && (
          <Box sx={{ padding: '12px 0px 0px 10px' }}>
            <Box key={'status_containers'} sx={{ marginBottom: '10px' }}>
              <AccordionSmall header={'Container Status'}>
                <CustomTable headers={headers} tableData={data} totalRows={data?.length} rowsPerPage={data?.length} />
              </AccordionSmall>
            </Box>
          </Box>
        )}
      </>
    );
  };

  const getStatusInfoInitContainerStatus = () => {
    let headers = ['Name', 'State', 'State Reason', 'State Started', 'State Finished', 'Restart Count', 'Ready', 'Exit Code', 'Started'];
    const initContainerStatues = pod?.meta?.status_info?.initContainerStatuses ?? pod?.meta?.status_info?.init_container_statuses;
    let data = initContainerStatues
      ?.map((item) => {
        let states = Object.keys(item.state ?? {}) ?? '';
        states = states.filter((f) => item.state[f]);
        return states.map((state) => {
          return [
            {
              text: item.name,
            },
            {
              text: state,
            },
            {
              text: item.state[state]?.reason,
            },
            {
              component: <Datetime value={item.state[state]?.startedAt ?? item.state[state]?.started_at} />,
            },
            {
              component: <Datetime value={item.state[state]?.finishedAt ?? item.state[state]?.finished_at} />,
            },
            {
              text: item.restartCount ?? item.restart_count ?? '-',
            },
            {
              text: item.ready ? 'True' : 'False',
            },
            {
              text: item.state[state]?.exit_code ?? '-',
            },
            {
              text: item.started ? 'True' : 'False',
            },
          ];
        });
      })
      .flat();
    return (
      <>
        {data?.length > 0 && (
          <Box sx={{ padding: '12px 0px 0px 10px' }}>
            <Box key={'status_inti_containers'} sx={{ marginBottom: '10px' }}>
              <AccordionSmall header={'Init Container Status'}>
                <KubernetesTable2 headers={headers} data={data} totalRows={data?.length} rowsPerPage={data?.length} />
              </AccordionSmall>
            </Box>
          </Box>
        )}
      </>
    );
  };
  // const router = useRouter();

  return (
    <>
      <Box
        sx={{
          background: 'white',
          marginTop: '16px',
          marginBottom: '24px',
          padding: '20px 24px',
          borderRadius: '10px',
          border: `1px solid ${colors.border.secondaryLight}`,
          display: 'flex',
          flexDirection: 'row',
          justifyContent: 'center',
        }}
      >
        <Box sx={{ flex: 1 }}>
          <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
            <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
              Name:
            </Typography>
            <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '400', lineHeight: '20px', color: '#2563EB', maxWidth: '270px' }}>
              {pod?.name}
            </Typography>
          </Box>
          <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
            <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
              Status:
            </Typography>
            <CustomLabels margin='0px' variant={getPodStatus(pod?.meta?.status).color} text={getPodStatus(pod?.meta?.status).status} />
          </Box>
          <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
            <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
              Created:
            </Typography>
            <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '400', lineHeight: '20px', color: '#737373', maxWidth: '270px' }}>
              {pod?.created_at}
            </Typography>
          </Box>
          <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
            <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
              Pod IP:
            </Typography>
            <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '400', lineHeight: '20px', color: '#737373', maxWidth: '270px' }}>
              {pod?.meta?.status_info?.podIPs?.map((f) => f.ip)?.join(',') ?? pod?.meta?.status_info?.pod_i_ps?.map((f) => f.ip)?.join(',') ?? '-'}
            </Typography>
          </Box>
          <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
            <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
              Controlled by:
            </Typography>
            <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '400', lineHeight: '20px', color: '#737373', maxWidth: '270px' }}>
              {pod?.meta?.controller ?? '-'}
            </Typography>
          </Box>
          <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
            <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
              Parent Controller:
            </Typography>
            <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '400', lineHeight: '20px', color: '#737373', maxWidth: '270px' }}>
              {pod?.meta?.config?.owner[0]?.name}
            </Typography>
          </Box>

          <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
            <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
              Node:
            </Typography>
            <ReactLink
              href={`/kubernetes/details/${accountId}?accountId=${accountId}&nodeName=${pod?.meta?.node}#kubernetes/nodes`}
              onClick={(e) => {
                e.stopPropagation();
              }}
              style={{ color: '#2563EB', fontSize: '14px', fontWeight: '400' }}
            >
              {pod?.meta?.node ?? '-'}
            </ReactLink>
          </Box>
          <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
            <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
              Namespace:
            </Typography>
            <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '400', lineHeight: '20px', color: '#737373' }}>
              {pod?.meta?.namespace}
            </Typography>
          </Box>
          <Box sx={{ display: 'flex', flex: 1, marginBottom: '8px' }}>
            <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
              QoS Class:
            </Typography>
            <Typography sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '400', lineHeight: '20px', color: '#737373' }}>
              {(pod?.meta?.config?.qos_class || pod?.meta?.status_info?.qosClass) ?? '-'}
            </Typography>
          </Box>
        </Box>
        <Divider sx={{ backgroundColor: '#D9D9D9', mx: '23px' }} variant={'middle'} orientation='vertical' />
        <Box sx={{ flex: 1 }}>
          <Grid container sx={{ marginBottom: '8px' }}>
            <Grid item md={3}>
              <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
                Labels:
              </Typography>
            </Grid>
            <Grid
              item
              md={9}
              sx={{
                display: 'flex',
                flexDirection: 'row',
                flexWrap: 'wrap',
                gap: '12px',
                fontFamily: 'Roboto',
                fontSize: '14px',
                fontWeight: '400',
                lineHeight: '20px',
                color: '#2563EB',
                maxWidth: '360px',
              }}
            >
              {mapLabels(pod?.meta?.config?.labels)}
            </Grid>
          </Grid>
          <Grid container sx={{ marginBottom: '8px' }}>
            <Grid item md={3}>
              <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
                Annotations:
              </Typography>
            </Grid>
            <Grid
              item
              md={9}
              sx={{
                display: 'flex',
                flexDirection: 'row',
                flexWrap: 'wrap',
                fontFamily: 'Roboto',
                gap: '12px',
                fontSize: '14px',
                fontWeight: '400',
                lineHeight: '20px',
                color: '#2563EB',
                maxWidth: '360px',
              }}
            >
              {mapLabels(pod?.meta?.config?.annotations)}
            </Grid>
          </Grid>
          <Grid container sx={{ marginBottom: '8px' }}>
            <Grid item md={3}>
              <Typography width={'150px'} sx={{ fontFamily: 'Roboto', fontSize: '14px', fontWeight: '500', lineHeight: '20px', color: '#374151' }}>
                Conditions:
              </Typography>
            </Grid>
            <Grid
              item
              md={9}
              sx={{
                display: 'flex',
                flexDirection: 'row',
                flexWrap: 'wrap',
                fontFamily: 'Roboto',
                fontSize: '14px',
                gap: '12px',
                fontWeight: '500',
                lineHeight: '20px',
                color: '#2563EB',
                maxWidth: '270px',
              }}
            >
              {pod?.meta?.config?.conditions
                ? pod?.meta?.config?.conditions.map((item) => {
                    return <CustomLabels textTransform='none' margin='0px' key={item.id} text={item?.type + ': ' + item?.status} variant={'green'} />;
                  })
                : ''}
            </Grid>
          </Grid>
        </Box>
      </Box>

      <Box marginBottom={'28px'}>
        <TextWithBorder
          value='Containers'
          borderColor={colors.text.primaryDark}
          borderWidth='3px'
          sx={{ '& p': { fontSize: '16px', fontWeight: 600, color: colors.text.secondary } }}
        />
        <Box sx={{ padding: '12px 0px 0px 10px' }}>
          <MapContainerHeader containers={pod?.meta?.config?.containers} />
        </Box>
      </Box>
      {tolerationRows?.length > 0 && (
        <Box marginBottom={'28px'}>
          <TextWithBorder
            value='Tolerations'
            borderColor={colors.text.primaryDark}
            borderWidth='3px'
            sx={{ '& p': { fontSize: '16px', fontWeight: 600, color: colors.text.secondary } }}
          />
          <Box sx={{ padding: '12px 0px 0px 10px' }}>
            <KubernetesTable2
              headers={TOLERATION_HEADERS}
              data={tolerationRows}
              totalRows={tolerationRows?.length}
              rowsPerPage={tolerationRows?.length}
            />
          </Box>
        </Box>
      )}
      <Box marginBottom={'28px'}>
        <TextWithBorder
          value='Volumes'
          borderColor={colors.text.primaryDark}
          borderWidth='3px'
          sx={{ '& p': { fontSize: '16px', fontWeight: 600, color: colors.text.secondary } }}
        />
        <Box sx={{ padding: '12px 0px 0px 10px' }}>
          <MapVolumeHeader volumes={pod?.meta?.config?.volumes} />
        </Box>
      </Box>
      <Box marginBottom={'28px'}>
        <TextWithBorder
          value='Status Info'
          borderColor={colors.text.primaryDark}
          borderWidth='3px'
          sx={{ '& p': { fontSize: '16px', fontWeight: 600, color: colors.text.secondary } }}
        />
        {getStatusInfoConditions()}
        {getStatusInfoContainerStatus()}
        {getStatusInfoInitContainerStatus()}
      </Box>
    </>
  );
};

export default PodDetailsBox;

PodDetailsBox.propTypes = {
  wordBreak: PropTypes.string,
  pod: PropTypes.any,
  accountId: PropTypes.string,
};
