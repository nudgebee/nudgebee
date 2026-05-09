import { colors } from '@utils/colors';
import { Grid, Typography } from '@mui/material';
import Stack from '@mui/material/Stack';
import React, { useEffect } from 'react';
import Box from '@mui/material/Box';
import apiUserManagement from '@api1/user';
import { useSession } from 'next-auth/react';
import cache from '@lib/cache';
import PropTypes from 'prop-types';
import { v4 as uuidv4 } from 'uuid';
import CustomButton from '@common/NewCustomButton';
import NDialog from '@common/modal/NDialog';
import FilterDropdownButton from '@common/FilterDropdownButton';

export const SwitchTenant = ({ open, title, onClose, buttonTitle = 'Switch Tenant' }) => {
  const [tenantValue, setTenantValue] = React.useState({ name: '' });
  const [tenants, setTenants] = React.useState([]);
  const [isTenantLoading, setIsTenantLoading] = React.useState(false);

  const { data, update } = useSession({
    required: true,
    onUnauthenticated: () => {},
  });

  const getUserData = async function () {
    if (data?.user?.email) {
      setIsTenantLoading(true);
      try {
        let tenantList;
        if (data?.isSuperAdmin || data?.isSuperAdminReadonly) {
          // Super admin: fetch ALL tenants via Hasura action
          const res = await apiUserManagement.listAllTenants();
          tenantList = res.data ?? [];
        } else {
          // Normal user: fetch only their tenants
          const res = await apiUserManagement.listUserTenants(data?.user?.email);
          tenantList = res.data ?? [];
        }

        // Assign unique id to each tenant for autocomplete tracking
        tenantList = tenantList.map((t) => ({ ...t, id: uuidv4() }));

        if (tenantList.length > 0) {
          let sorted = tenantList.sort((a, b) => a.name.localeCompare(b.name));
          setTenants(sorted);
          setTenantValue(sorted.filter((t) => t.name == data?.tenant?.name)[0] || sorted[0]);
        } else {
          setTenants([]);
          setTenantValue({ name: '' });
        }
      } finally {
        setIsTenantLoading(false);
      }
    }
  };

  const updateUserTenant = async function () {
    await update({ ...data, tenantName: tenantValue.name });
    cache.clear();
    // route.push('/home'); is not working correctly, its adding previous accountIds as well for some reasons
    window.location.href = '/home';
  };

  useEffect(() => {
    if (data?.user?.email && open) {
      getUserData();
    }
  }, [data?.user?.email, open]);

  return (
    <NDialog
      buttonText={buttonTitle}
      handleClose={onClose}
      backdropClickClose={false}
      dialogTitle={
        <Typography component='h2' variant='h5' fontWeight={600} color={colors.text.signinDark}>
          {title} - {tenantValue?.name || data?.tenant?.name}
        </Typography>
      }
      handleSubmit={() => {
        updateUserTenant();
      }}
      isCancelRequired={false}
      isSubmitRequired={false}
      open={open}
      dialogContent={
        <Box display='flex' flexDirection='column' justifyContent='space-between' alignItems='left'>
          <Grid item xs={12} mt={2}>
            <FilterDropdownButton
              label={'Tenant'}
              value={tenantValue}
              options={tenants ?? []}
              disabled={tenants?.length === 0 || isTenantLoading}
              onSelect={(_event, value) => {
                const newEventValue = value?.value || value;
                setTenantValue(newEventValue);
              }}
              isOptionsLoading={isTenantLoading}
            />
          </Grid>
          <Box
            sx={{
              borderBottom: `1px solid ${colors.border.secondary}`,
              margin: '16px 0px',
            }}
          />
          <Stack
            spacing={1}
            direction='row'
            justifyContent={'flex-end'}
            sx={{
              float: 'right',
              button: {
                minWidth: '120px',
              },
            }}
          >
            <CustomButton text={'Cancel'} variant='secondary' size={'Medium'} onClick={onClose} />
            <CustomButton
              type='submit'
              text={buttonTitle}
              size='Medium'
              onClick={() => {
                updateUserTenant();
              }}
            />
          </Stack>
        </Box>
      }
      additionalComponent={undefined}
      width='sm'
    />
  );
};

SwitchTenant.propTypes = {
  open: PropTypes.bool,
  title: PropTypes.string,
  onClose: PropTypes.func,
  buttonTitle: PropTypes.string,
};
