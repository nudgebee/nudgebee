import React, { useState, useEffect } from 'react';
import PropTypes from 'prop-types';
import { Tabs, Tab, Box, Typography } from '@mui/material';
import KubernetesLLMRequestResponse from './KubernetesLLMRequestResponseV2';
import ConversationCollapsableCard from './common/ConversationCollapsableCard';
import SafeIcon from '@components1/common/SafeIcon';
import { FinalResponseIcon } from '@assets';
import apiAskNudgebee from '@api1/ask-nudgebee';
import CustomTable from '@common-new/tables/CustomTable2';
import { convertToReadableFormat } from 'src/utils/common';
import { ds } from '@utils/colors';
import Text from '@common-new/format/Text';

/**
 * LLMConversationWithTabs
 * Displays LLM conversation in two tabs: Tasks (steps) and Final Response.
 * Minimal, modern tab UI. No ConversationList or multi-session logic.
 */
const LLMConversationWithTabs = ({
  messages = [],
  _isLoading = false,
  accountId,
  handleShare,
  sessionId,
  conversationId,
  generateQuestionText,
  showFullTextHandler,
  showFullText,
  handleCardClick,
  collapsedObj,
  getCardTitle,
}) => {
  // Default to Final Response tab when available
  const [activeTab, setActiveTab] = useState('response');
  const [references, setReferences] = useState([]);
  const [memories, setMemories] = useState([]);

  // Separate messages into user prompt, tasks, and final response
  const hasMessages = Array.isArray(messages) && messages.length > 0;
  const finalResponse = hasMessages ? messages.find((m) => m?.type === 'response') : null;
  const userPrompt = hasMessages ? messages.find((m) => m?.type === 'question') : null;
  const tasks = hasMessages ? messages.filter((m) => m?.type !== 'response' && m?.type !== 'question') : [];

  const [fetchedMessageId, setFetchedMessageId] = useState(null);
  const lastSeenResponseId = React.useRef(null);

  // Auto-select Final Response tab only on initial load when it becomes available
  useEffect(() => {
    if (finalResponse?.id && lastSeenResponseId.current !== finalResponse.id) {
      setActiveTab('response');
      lastSeenResponseId.current = finalResponse.id;
      // Reset for new message
      setReferences([]);
      setMemories([]);
      setFetchedMessageId(null);
    }
  }, [finalResponse?.id]);

  useEffect(() => {
    if (!finalResponse?.id) {
      return;
    }

    const isCompleted = ['COMPLETED', 'SUCCESS'].includes(finalResponse.status?.toUpperCase());

    if (isCompleted && finalResponse.id !== fetchedMessageId) {
      setFetchedMessageId(finalResponse.id);

      Promise.all([
        apiAskNudgebee.listReferences({
          accountId,
          messageId: finalResponse.id,
          conversationId,
        }),
        apiAskNudgebee.listMemory(accountId, conversationId, finalResponse.id),
      ])
        .then(([refRes, memRes]) => {
          setReferences(refRes?.data || []);
          setMemories(memRes?.data || []);
        })
        .catch((err) => {
          console.error('Failed to fetch additional turn data', err);
          setReferences([]);
          setMemories([]);
        });
    }
  }, [finalResponse, accountId, conversationId, fetchedMessageId]);

  const getTableData = (data) => {
    if (data && data.length > 0) {
      const headers = Object.keys(data[0]);
      // Check all rows for new headers
      for (let i = 1; i < data.length; i++) {
        const rowKeys = Object.keys(data[i]);
        for (const key of rowKeys) {
          if (!headers.includes(key)) {
            headers.push(key);
          }
        }
      }

      const tableData = data.map((row) => {
        return headers.map((header) => {
          let value = row[header];
          if (typeof value === 'object' || Array.isArray(value)) {
            value = JSON.stringify(value);
          }
          return {
            component: <Text value={value} showAutoEllipsis sx={{ minWidth: ds.space.mul(0, 25) }} />,
          };
        });
      });
      return {
        headers: headers.map((f) => convertToReadableFormat(f.replaceAll('_', ' '))),
        tableData,
      };
    }
    return { headers: [], tableData: [] };
  };

  return (
    <Box
      sx={{
        mt: ds.space[2],
        mr: ds.space.mul(0, 100),
        width: '100%',
        bgcolor: 'var(--ds-background-100)',
        borderRadius: ds.radius.lg,
        boxShadow: '0 2px 8px rgba(0, 0, 0, 0.08)',
      }}
    >
      <Tabs
        value={activeTab}
        onChange={(event, newValue) => {
          setActiveTab(newValue);
        }}
        variant='fullWidth'
        centered
        sx={{
          mb: ds.space[5],
          borderBottom: `1px solid var(--ds-blue-500)`,
          '& .MuiTabs-flexContainer': {
            justifyContent: 'center',
          },
          '& .MuiTab-root': {
            fontWeight: 'var(--ds-font-weight-medium)',
            fontSize: '0.95rem',
            textTransform: 'none',
          },
        }}
      >
        <Tab value='response' label={<Typography>Response</Typography>} disabled={!finalResponse} />
        <Tab value='tasks' label={<Typography>Tasks ({tasks?.length || 0})</Typography>} />
        <Tab value='prompt' label={<Typography>User Prompt</Typography>} disabled={!userPrompt} />
        {references.length > 0 && <Tab value='contexts' label={<Typography>Additional Contexts ({references.length})</Typography>} />}
        {memories.length > 0 && <Tab value='memories' label={<Typography>New Memories ({memories.length})</Typography>} />}
      </Tabs>
      <Box sx={{ minHeight: ds.space.mul(2, 15), width: '100%', mx: 'auto', maxWidth: '98%' }}>
        <>
          {/* Final Response Tab */}
          {activeTab === 'response' && (
            <Box sx={{ width: '100%', height: 'auto' }}>
              {finalResponse ? (
                <ConversationCollapsableCard
                  key={finalResponse?.tool_id}
                  idx={messages.findIndex((m) => m.type === 'response')}
                  showFullTextHandler={showFullTextHandler}
                  showFullText={showFullText}
                  textLength={false}
                  text={
                    <Box sx={{ display: 'flex', alignItems: 'start', flexDirection: 'column', justifyContent: 'space-between', gap: ds.space[0] }}>
                      <Box sx={{ display: 'flex', alignItems: 'center', my: ds.space.mul(0, 5) }}>
                        <SafeIcon src={FinalResponseIcon} alt='response' height={24} />
                      </Box>
                    </Box>
                  }
                  contentComponents={
                    <KubernetesLLMRequestResponse
                      toolCall={finalResponse}
                      messages={messages}
                      generateQuestionText={generateQuestionText}
                      accountId={accountId}
                      handleShare={handleShare}
                      sessionId={sessionId}
                      conversationId={conversationId}
                    />
                  }
                  onCardClick={() => {
                    return;
                  }}
                  collapsedObj={{}}
                  isCollapsed={false}
                  toolData={finalResponse}
                  isCollapsible={false}
                  conversationCreatedAt={finalResponse?.created_at}
                  conversationUpdatedAt={finalResponse?.updated_at}
                />
              ) : (
                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', textAlign: 'center', mt: ds.space[4] }}>
                  Final response not available yet.
                </Typography>
              )}
            </Box>
          )}
          {/* User Prompt Tab */}
          {activeTab === 'prompt' && (
            <Box sx={{ display: 'block', width: '100%', height: 'auto', border: '1px solid transparent' }}>
              {userPrompt ? (
                <ConversationCollapsableCard
                  key={userPrompt?.tool_id}
                  idx={messages.findIndex((m) => m.type === 'question')}
                  showFullTextHandler={showFullTextHandler}
                  showFullText={showFullText}
                  textLength={false}
                  text={
                    <Typography variant='subtitle1' fontWeight='400'>
                      {userPrompt?.text}
                    </Typography>
                  }
                  contentComponents={<Box />}
                  onCardClick={() => {
                    return;
                  }}
                  collapsedObj={{}}
                  isCollapsed={false}
                  toolData={userPrompt}
                  isCollapsible={false}
                  conversationCreatedAt={userPrompt?.created_at}
                  conversationUpdatedAt={userPrompt?.updated_at}
                />
              ) : (
                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', textAlign: 'center', mt: ds.space[4] }}>
                  No user prompt available.
                </Typography>
              )}
            </Box>
          )}

          {/* Tasks Tab */}
          {activeTab === 'tasks' && (
            <Box sx={{ display: 'block', width: '100%', height: 'auto', border: '1px solid transparent' }}>
              {tasks.length === 0 ? (
                <Typography sx={{ fontSize: 'var(--ds-text-small)', color: 'var(--ds-gray-500)', textAlign: 'center', mt: ds.space[4] }}>
                  No intermediate steps.
                </Typography>
              ) : (
                <Box>
                  {tasks.map((task, index) => (
                    <Box key={task.id || index} sx={{ mb: ds.space[4] }}>
                      <ConversationCollapsableCard
                        key={task?.tool_id}
                        idx={index}
                        showFullTextHandler={showFullTextHandler}
                        showFullText={showFullText}
                        textLength={false}
                        text={
                          <Box
                            sx={{ display: 'flex', alignItems: 'start', flexDirection: 'column', justifyContent: 'space-between', gap: ds.space[0] }}
                          >
                            {getCardTitle && getCardTitle(task)}
                          </Box>
                        }
                        contentComponents={
                          <KubernetesLLMRequestResponse
                            toolCall={task}
                            messages={messages}
                            generateQuestionText={generateQuestionText}
                            accountId={accountId}
                            handleShare={handleShare}
                            sessionId={sessionId}
                            conversationId={conversationId}
                          />
                        }
                        onCardClick={handleCardClick}
                        collapsedObj={collapsedObj}
                        isCollapsed={collapsedObj && collapsedObj[index]}
                        toolData={task}
                        isCollapsible={task?.type !== 'question' && task?.type !== 'acknowledgment'}
                        conversationCreatedAt={task?.created_at}
                        conversationUpdatedAt={task?.updated_at}
                      />
                    </Box>
                  ))}
                </Box>
              )}
            </Box>
          )}

          {/* Additional Contexts Tab */}
          {activeTab === 'contexts' && references.length > 0 && (
            <Box sx={{ width: '100%', height: 'auto', overflowX: 'auto' }}>
              {(() => {
                const { headers, tableData } = getTableData(
                  references.map(({ content, metadata, type, created_at }) => ({
                    content,
                    type,
                    created_at,
                    ...metadata,
                  }))
                );
                return (
                  <CustomTable
                    tableData={tableData}
                    headers={headers}
                    totalRows={tableData.length}
                    rowsPerPage={10}
                    renderVertical={tableData?.length <= 1}
                  />
                );
              })()}
            </Box>
          )}

          {/* New Memories Tab */}
          {activeTab === 'memories' && memories.length > 0 && (
            <Box sx={{ width: '100%', height: 'auto', overflowX: 'auto' }}>
              {(() => {
                const { headers, tableData } = getTableData(
                  memories.map(({ content, memory_type, created_at }) => ({
                    content,
                    memory_type,
                    created_at,
                  }))
                );
                return (
                  <CustomTable
                    tableData={tableData}
                    headers={headers}
                    totalRows={tableData.length}
                    rowsPerPage={10}
                    renderVertical={tableData?.length <= 1}
                  />
                );
              })()}
            </Box>
          )}
        </>
      </Box>
    </Box>
  );
};

LLMConversationWithTabs.propTypes = {
  messages: PropTypes.array,
  isLoading: PropTypes.bool,
  accountId: PropTypes.string,
  handleShare: PropTypes.func,
  sessionId: PropTypes.string,
  conversationId: PropTypes.string,
  generateQuestionText: PropTypes.string,
  showFullTextHandler: PropTypes.func,
  showFullText: PropTypes.bool,
  handleCardClick: PropTypes.func,
  collapsedObj: PropTypes.object,
  getCardTitle: PropTypes.func,
};

export default LLMConversationWithTabs;
