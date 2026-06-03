import React, { useEffect, useState } from 'react';
import { Box, Typography, Chip } from '@mui/material';
import apiCloudAccount from '@api1/cloud-account';
import SummarySkeletonLoader from '@components1/common/SummarySkeletonLoader';
import { ds } from '@utils/colors';

const parseResource = (r: any) => ({
  ...r,
  meta: typeof r.meta === 'string' ? JSON.parse(r.meta || '{}') : r.meta || {},
  tags: typeof r.tags === 'string' ? JSON.parse(r.tags || '{}') : r.tags || {},
});

const Card = ({ children, sx = {} }: any) => (
  <Box
    sx={{
      backgroundColor: ds.background[100],
      borderRadius: ds.radius.md,
      boxShadow: '0px 4px 6px -1px rgba(0, 0, 0, 0.05), 0px 2px 4px -2px rgba(0, 0, 0, 0.05)',
      py: ds.space[4],
      px: ds.space[5],
      ...sx,
    }}
  >
    {children}
  </Box>
);

const StatCard = ({ label, value, subtext }: { label: string; value: string | number; subtext?: string }) => (
  <Card sx={{ textAlign: 'center', minHeight: '90px', display: 'flex', flexDirection: 'column', justifyContent: 'center', alignItems: 'center' }}>
    <Typography sx={{ color: ds.gray[500], fontSize: ds.text.small, mb: ds.space[1] }}>{label}</Typography>
    <Typography sx={{ color: ds.gray[700], fontSize: ds.text.heading, fontWeight: ds.weight.semibold }}>{value}</Typography>
    {subtext && <Typography sx={{ color: ds.gray[400], fontSize: ds.text.caption, mt: ds.space[1] }}>{subtext}</Typography>}
  </Card>
);

const StatusDot = ({ active }: { active: boolean }) => (
  <Box sx={{ width: 8, height: 8, borderRadius: '50%', backgroundColor: active ? ds.green[500] : ds.red[500], flexShrink: 0 }} />
);

// --- Organizations Summary ---
const OrgSummaryContent = ({ resources, apps }: { resources: any[]; apps: any[] }) => {
  const orgStats: Record<string, { appCount: number; spaceCount: number; memoryMB: number; runningApps: number }> = {};
  resources.forEach((org: any) => {
    orgStats[org.name] = { appCount: 0, spaceCount: 0, memoryMB: 0, runningApps: 0 };
  });

  apps.forEach((app: any) => {
    const orgTag = app.tags?.org;
    const orgName = Array.isArray(orgTag) ? orgTag[0] : orgTag || '';
    if (orgName && orgStats[orgName]) {
      orgStats[orgName].appCount += 1;
      const mem = app.meta?.memory_in_mb || 0;
      const inst = app.meta?.instances || 0;
      orgStats[orgName].memoryMB += mem * inst;
      if (app.status?.toLowerCase() === 'active') orgStats[orgName].runningApps += 1;
    }
  });

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4] }}>
      <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: ds.space[3] }}>
        <StatCard label='Total Organizations' value={resources.length} />
        <StatCard label='Total Apps' value={apps.length} subtext={`across all orgs`} />
        <StatCard label='Total Memory' value={`${Object.values(orgStats).reduce((s, o) => s + o.memoryMB, 0)} MB`} subtext='allocated' />
      </Box>
      <Card>
        <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[3] }}>
          Organizations Overview
        </Typography>
        {resources.map((org: any, idx: number) => {
          const stats = orgStats[org.name] || { appCount: 0, memoryMB: 0, runningApps: 0 };
          return (
            <Box
              key={org.resourse_id || org.name}
              sx={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                py: ds.space[3],
                borderBottom: idx < resources.length - 1 ? `1px solid ${ds.gray[100]}` : 'none',
              }}
            >
              <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
                <StatusDot active={true} />
                <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.medium, color: ds.gray[700] }}>{org.name}</Typography>
              </Box>
              <Box sx={{ display: 'flex', gap: ds.space[4] }}>
                <Chip label={`${stats.appCount} apps`} size='small' sx={{ fontSize: ds.text.caption, height: 22 }} />
                <Chip
                  label={`${stats.runningApps} running`}
                  size='small'
                  color='success'
                  variant='outlined'
                  sx={{ fontSize: ds.text.caption, height: 22 }}
                />
                {stats.memoryMB > 0 && (
                  <Chip label={`${stats.memoryMB} MB`} size='small' variant='outlined' sx={{ fontSize: ds.text.caption, height: 22 }} />
                )}
              </Box>
            </Box>
          );
        })}
      </Card>
    </Box>
  );
};

// --- Spaces Summary ---
const SpaceSummaryContent = ({ resources, apps }: { resources: any[]; apps: any[] }) => {
  const spaceStats: Record<string, { appCount: number; memoryMB: number; runningApps: number; orgName: string }> = {};
  resources.forEach((space: any) => {
    const orgTag = space.tags?.org;
    const orgName = Array.isArray(orgTag) ? orgTag[0] : orgTag || space.meta?.org_name || space.region || '-';
    spaceStats[space.name] = { appCount: 0, memoryMB: 0, runningApps: 0, orgName };
  });

  apps.forEach((app: any) => {
    const spaceTag = app.tags?.space;
    const spaceName = Array.isArray(spaceTag) ? spaceTag[0] : spaceTag || '';
    if (spaceName && spaceStats[spaceName]) {
      spaceStats[spaceName].appCount += 1;
      const mem = app.meta?.memory_in_mb || 0;
      const inst = app.meta?.instances || 0;
      spaceStats[spaceName].memoryMB += mem * inst;
      if (app.status?.toLowerCase() === 'active') spaceStats[spaceName].runningApps += 1;
    }
  });

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4] }}>
      <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: ds.space[3] }}>
        <StatCard label='Total Spaces' value={resources.length} />
        <StatCard label='Total Apps' value={apps.length} subtext='across all spaces' />
        <StatCard label='Total Memory' value={`${Object.values(spaceStats).reduce((s, o) => s + o.memoryMB, 0)} MB`} subtext='allocated' />
      </Box>
      <Card>
        <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[3] }}>Spaces Overview</Typography>
        {resources.map((space: any, idx: number) => {
          const stats = spaceStats[space.name] || { appCount: 0, memoryMB: 0, runningApps: 0, orgName: '-' };
          return (
            <Box
              key={space.resourse_id || space.name}
              sx={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                py: ds.space[3],
                borderBottom: idx < resources.length - 1 ? `1px solid ${ds.gray[100]}` : 'none',
              }}
            >
              <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
                <StatusDot active={true} />
                <Box>
                  <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.medium, color: ds.gray[700] }}>{space.name}</Typography>
                  <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>org: {stats.orgName}</Typography>
                </Box>
              </Box>
              <Box sx={{ display: 'flex', gap: ds.space[4] }}>
                <Chip label={`${stats.appCount} apps`} size='small' sx={{ fontSize: ds.text.caption, height: 22 }} />
                <Chip
                  label={`${stats.runningApps} running`}
                  size='small'
                  color='success'
                  variant='outlined'
                  sx={{ fontSize: ds.text.caption, height: 22 }}
                />
                {stats.memoryMB > 0 && (
                  <Chip label={`${stats.memoryMB} MB`} size='small' variant='outlined' sx={{ fontSize: ds.text.caption, height: 22 }} />
                )}
              </Box>
            </Box>
          );
        })}
      </Card>
    </Box>
  );
};

// --- Routes Summary with App Connections ---
const RouteSummaryContent = ({ resources, apps }: { resources: any[]; apps: any[] }) => {
  // Build app GUID -> name map for route destination resolution
  const appMap: Record<string, { name: string; status: string }> = {};
  apps.forEach((app: any) => {
    if (app.resourse_id) {
      appMap[app.resourse_id] = { name: app.name || app.resourse_id, status: app.status || '' };
    }
  });

  const routesWithDests = resources.filter((r: any) => r.meta?.destination_apps?.length > 0);
  const routesWithoutDests = resources.filter((r: any) => !r.meta?.destination_apps?.length);

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[4] }}>
      <Box sx={{ display: 'grid', gridTemplateColumns: 'repeat(3, 1fr)', gap: ds.space[3] }}>
        <StatCard label='Total Routes' value={resources.length} />
        <StatCard label='Mapped Routes' value={routesWithDests.length} subtext='with app bindings' />
        <StatCard label='Unmapped Routes' value={routesWithoutDests.length} subtext='no app bindings' />
      </Box>

      {/* App-Route Connection Map */}
      <Card>
        <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, color: ds.gray[700], mb: ds.space[3] }}>
          App ↔ Route Connections
        </Typography>
        {resources.length === 0 ? (
          <Typography sx={{ color: ds.gray[500], fontSize: ds.text.small, textAlign: 'center', py: ds.space[4] }}>No routes found</Typography>
        ) : (
          resources.map((route: any, idx: number) => {
            const url = route.meta?.url || route.name || '-';
            const protocol = route.meta?.protocol || 'http';
            const destApps: string[] = route.meta?.destination_apps || [];
            const spaceTag = route.tags?.space;
            const space = Array.isArray(spaceTag) ? spaceTag[0] : spaceTag || '-';

            const getAppDotColor = (info: any, active: boolean) => {
              if (!info) return ds.gray[300];
              return active ? ds.green[500] : ds.red[500];
            };
            const getAppBorderColor = (info: any, active: boolean) => {
              if (!info) return ds.gray[200];
              return active ? ds.green[200] : ds.red[200];
            };

            return (
              <Box
                key={route.resourse_id || route.name}
                sx={{
                  py: ds.space[3],
                  borderBottom: idx < resources.length - 1 ? `1px solid ${ds.gray[100]}` : 'none',
                }}
              >
                <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: destApps.length > 0 ? ds.space[2] : 0 }}>
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
                    <Chip label={protocol} size='small' variant='outlined' sx={{ fontSize: ds.text.caption, height: 20, fontFamily: ds.font.mono }} />
                    <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.medium, color: ds.gray[700], fontFamily: ds.font.mono }}>
                      {url}
                    </Typography>
                  </Box>
                  {space !== '-' && <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>space: {space}</Typography>}
                </Box>
                {destApps.length > 0 ? (
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], ml: ds.space[5], flexWrap: 'wrap' }}>
                    <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], mr: ds.space[1] }}>→</Typography>
                    {destApps.map((appGuid: string) => {
                      const appInfo = appMap[appGuid];
                      const isActive = appInfo?.status?.toLowerCase() === 'active';
                      return (
                        <Chip
                          key={appGuid}
                          icon={
                            <Box
                              sx={{
                                width: 6,
                                height: 6,
                                borderRadius: '50%',
                                backgroundColor: getAppDotColor(appInfo, isActive),
                                ml: ds.space[1],
                              }}
                            />
                          }
                          label={appInfo?.name || appGuid.substring(0, 8) + '...'}
                          size='small'
                          variant='outlined'
                          sx={{
                            fontSize: ds.text.caption,
                            height: 22,
                            borderColor: getAppBorderColor(appInfo, isActive),
                          }}
                        />
                      );
                    })}
                  </Box>
                ) : (
                  <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[300], ml: ds.space[5], fontStyle: 'italic' }}>
                    No apps bound to this route
                  </Typography>
                )}
              </Box>
            );
          })
        )}
      </Card>
    </Box>
  );
};

// --- Main Component ---
const CFResourceSummary = ({ accountId, serviceName }: { accountId: string; serviceName: string }) => {
  const [loading, setLoading] = useState(true);
  const [resources, setResources] = useState<any[]>([]);
  const [apps, setApps] = useState<any[]>([]);

  useEffect(() => {
    if (!accountId) return;
    setLoading(true);

    const fetchResources = apiCloudAccount.getCloudResource({ account_id: accountId, serviceName, type: [] }, 500, 0);
    const fetchApps = apiCloudAccount.getCloudResource({ account_id: accountId, serviceName: 'apps', type: [] }, 500, 0);

    Promise.all([fetchResources, fetchApps])
      .then(([resResult, appResult]: any[]) => {
        const resList = (resResult.data?.data?.cloud_resourses || []).map(parseResource);
        const appList = (appResult.data?.data?.cloud_resourses || []).map(parseResource);
        setResources(resList);
        setApps(appList);
        setLoading(false);
      })
      .catch((err: any) => {
        console.error('CFResourceSummary fetch error:', err);
        setLoading(false);
      });
  }, [accountId, serviceName]);

  if (loading) return <SummarySkeletonLoader />;

  if (serviceName === 'organizations') return <OrgSummaryContent resources={resources} apps={apps} />;
  if (serviceName === 'spaces') return <SpaceSummaryContent resources={resources} apps={apps} />;
  if (serviceName === 'routes') return <RouteSummaryContent resources={resources} apps={apps} />;

  return null;
};

export default CFResourceSummary;
