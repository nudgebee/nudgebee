import React, { useEffect, useMemo, useState, useCallback } from 'react';
import { Box, Typography } from '@mui/material';
import SearchIcon from '@mui/icons-material/Search';
import { Input } from '@ui/Input';
import { Chip } from '@ui/Chip';
import { Accordion } from '@ui/Accordion';
import { Banner } from '@ui/Banner';
import { Skeleton } from '@ui/Skeleton';
import { EmptyState } from '@ui/EmptyState';
import SafeIcon from '@shared/icons/SafeIcon';
import BoxLayout2 from '@shared/BoxLayout2';
import apiWorkflow from '@api1/workflow';
import { parseHttpResponseBodyMessage } from 'src/utils/common';
import { generateNodeCategories } from './constants/nodeCategories';
import ActionDetailsSidebar from './ActionDetailsSidebar';
import type { TaskDefinition } from '@components/workflow/types';

interface TaskRunnerProps {
  accountId: string;
}

const renderCategoryOrTaskIcon = (icon: any, label: string, size: number) => {
  if (typeof icon === 'string' && !icon.includes('/') && !icon.includes('.')) {
    // Emoji icon
    return <span style={{ fontSize: size >= 24 ? 'var(--ds-text-heading)' : 'var(--ds-text-title)' }}>{icon}</span>;
  }
  return <SafeIcon src={icon} alt={label} width={size} height={size} style={{ objectFit: 'contain' }} />;
};

const matchesAnyAlias = (aliases: string[] | undefined, query: string): boolean => {
  if (!aliases?.length) return false;
  return aliases.some((alias) => alias.toLowerCase().includes(query));
};

const TaskRunner: React.FC<TaskRunnerProps> = ({ accountId }) => {
  const [taskDefinitions, setTaskDefinitions] = useState<TaskDefinition[]>([]);
  const [loadingTasks, setLoadingTasks] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [selectedTaskType, setSelectedTaskType] = useState<string | null>(null);
  const [taskData, setTaskData] = useState<Record<string, any>>({});

  useEffect(() => {
    const fetchTasks = async () => {
      setLoadingTasks(true);
      setLoadError(null);
      try {
        const resp: any = await apiWorkflow.listTaskDefinitions();
        const errorMessage = parseHttpResponseBodyMessage(resp);
        if (errorMessage) {
          setLoadError(errorMessage);
        } else {
          const tasks: TaskDefinition[] = resp?.data?.workflow_list_taskdefinitions?.tasks || [];
          setTaskDefinitions(tasks);
        }
      } catch {
        setLoadError('Failed to load task definitions.');
      } finally {
        setLoadingTasks(false);
      }
    };
    fetchTasks();
  }, []);

  // Same category grouping/icons as the workflow builder's "Add Node" listing,
  // minus the static trigger entries (triggers can't be run standalone).
  const categories = useMemo(() => {
    const generated = generateNodeCategories(taskDefinitions);
    const { triggers: _triggers, ...rest } = generated as Record<string, any>;
    return rest;
  }, [taskDefinitions]);

  const filteredCategories = useMemo(() => {
    const query = search.trim().toLowerCase();
    if (!query) return categories;

    const filtered: Record<string, any> = {};
    Object.entries(categories).forEach(([categoryKey, category]: [string, any]) => {
      const categoryMatches = category.label.toLowerCase().includes(query);
      const matchingSubcategories: Record<string, any> = {};
      Object.entries(category.subcategories || {}).forEach(([subKey, sub]: [string, any]) => {
        if (
          sub.label.toLowerCase().includes(query) ||
          subKey.toLowerCase().includes(query) ||
          matchesAnyAlias(sub.aliases, query) ||
          categoryMatches
        ) {
          matchingSubcategories[subKey] = sub;
        }
      });
      if (Object.keys(matchingSubcategories).length > 0) {
        filtered[categoryKey] = { ...category, subcategories: matchingSubcategories };
      }
    });
    return filtered;
  }, [categories, search]);

  const handleSelectTask = (taskType: string) => {
    setTaskData({});
    setSelectedTaskType(taskType);
  };

  const handleRunTask = useCallback(
    async (taskType: string, params: any): Promise<any> => {
      try {
        const response: any = await apiWorkflow.triggerTask(accountId, taskType, params);
        const errorMessage = parseHttpResponseBodyMessage(response);
        if (errorMessage) {
          return { error: errorMessage };
        }
        const taskResult = response?.data?.workflow_execute_task;
        if (taskResult?.status === 'FAILED') {
          return { error: taskResult.error || 'Task execution failed' };
        }
        return taskResult?.result ?? taskResult;
      } catch (error: any) {
        console.error('Failed to run task:', error);
        return { error: error?.message || 'Failed to run task' };
      }
    },
    [accountId]
  );

  const accordionItems = useMemo(
    () =>
      Object.entries(filteredCategories).map(([categoryKey, category]: [string, any]) => ({
        id: categoryKey,
        icon: (
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              width: '40px',
              height: '40px',
              borderRadius: 'var(--ds-radius-lg)',
              backgroundColor: `color-mix(in srgb, ${category.color} 10%, transparent)`,
              border: `1px solid color-mix(in srgb, ${category.color} 35%, transparent)`,
              flexShrink: 0,
            }}
          >
            {renderCategoryOrTaskIcon(category.icon, category.label, 24)}
          </Box>
        ),
        label: category.label,
        description: category.description,
        meta: (
          <Chip variant='count' size='sm'>
            {String(Object.keys(category.subcategories || {}).length)}
          </Chip>
        ),
        body: (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
            {Object.entries(category.subcategories || {}).map(([taskType, sub]: [string, any]) => (
              <Box
                key={taskType}
                id={`task-runner-task-${taskType}-btn`}
                role='button'
                tabIndex={0}
                onClick={() => handleSelectTask(taskType)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    handleSelectTask(taskType);
                  }
                }}
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  padding: 'var(--ds-space-2) var(--ds-space-3)',
                  borderRadius: 'var(--ds-radius-lg)',
                  cursor: 'pointer',
                  transition: 'all 0.2s',
                  opacity: sub.deprecated ? 0.6 : 1,
                  '&:hover': { backgroundColor: 'var(--ds-background-200)' },
                }}
              >
                <Box sx={{ marginRight: 'var(--ds-space-3)', display: 'flex', alignItems: 'center', width: '24px', height: '24px', flexShrink: 0 }}>
                  {renderCategoryOrTaskIcon(sub.icon, sub.label, 20)}
                </Box>
                <Box sx={{ textAlign: 'left', flexGrow: 1, minWidth: 0 }}>
                  <Typography
                    sx={{
                      fontSize: 'var(--ds-text-body)',
                      fontWeight: 'var(--ds-font-weight-semibold)',
                      color: 'var(--ds-brand-500)',
                      fontFamily: 'poppins',
                    }}
                  >
                    {sub.label}
                  </Typography>
                  <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-600)' }}>{sub.description}</Typography>
                </Box>
              </Box>
            ))}
          </Box>
        ),
      })),
    [filteredCategories]
  );

  return (
    <BoxLayout2
      id='task-runner-box'
      heading='Task Runner'
      sharingOptions={{ sharing: { enabled: false, onClick: null }, download: { enabled: false, onClick: () => ({ tableId: '' }) } }}
    >
      <Box sx={{ display: 'flex', gap: 'var(--ds-space-4)', height: 'calc(100vh - 260px)', minHeight: '520px' }}>
        {/* LEFT — Task listing (20%) */}
        <Box className='custom-scrollbar' sx={{ width: '20%', minWidth: '320px', flexShrink: 0, overflowY: 'auto', pr: 'var(--ds-space-2)' }}>
          <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-600)', mb: 'var(--ds-space-4)' }}>
            Configure and run an individual automation action against this account.
          </Typography>

          <Box sx={{ mb: 'var(--ds-space-4)' }}>
            <Input
              id='task-runner-search-input'
              size='md'
              placeholder='Search actions...'
              value={search}
              onChange={setSearch}
              leadingIcon={<SearchIcon sx={{ fontSize: 'var(--ds-text-heading)' }} />}
            />
          </Box>

          {loadError && (
            <Box sx={{ mb: 'var(--ds-space-3)' }}>
              <Banner tone='critical' title='Failed to load tasks' message={loadError} />
            </Box>
          )}

          {loadingTasks ? (
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
              <Skeleton height={64} />
              <Skeleton height={64} />
              <Skeleton height={64} />
              <Skeleton height={64} />
            </Box>
          ) : accordionItems.length === 0 && !loadError ? (
            <EmptyState title='No matching tasks' description='Try a different search term.' />
          ) : (
            <Accordion items={accordionItems} selection='single' density='md' />
          )}
        </Box>

        {/* RIGHT — Action configuration + test (80%). Same configure-and-test
          panel as the workflow builder, embedded inline instead of a dialog:
          dynamic parameter form (API-backed dropdowns included) + Run. */}
        <Box sx={{ flex: 1, minWidth: 0, height: '100%' }}>
          {selectedTaskType ? (
            <ActionDetailsSidebar
              variant='inline'
              open
              onClose={() => setSelectedTaskType(null)}
              selectedActionType={selectedTaskType}
              nodes={[]}
              edges={[]}
              onTaskDataChange={(data: any) => setTaskData(data)}
              taskDefinitions={taskDefinitions}
              taskData={taskData}
              viewOnlyMode={false}
              accountId={accountId}
              onRunTask={handleRunTask}
            />
          ) : (
            <Box
              sx={{
                height: '100%',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                border: '1px dashed var(--ds-gray-300)',
                borderRadius: 'var(--ds-radius-lg)',
                backgroundColor: 'var(--ds-background-100)',
              }}
            >
              <EmptyState title='No task selected' description='Pick a task from the list to configure and run it.' />
            </Box>
          )}
        </Box>
      </Box>
    </BoxLayout2>
  );
};

export default TaskRunner;
