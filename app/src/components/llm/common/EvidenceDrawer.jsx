import React, { useCallback, useState } from 'react';
import PropTypes from 'prop-types';
import { Box } from '@mui/material';
import CustomDrawer, { SecondaryDrawer } from '@shared/CustomDrawer';
import ConversationLoader from '@shared/ConversationLoader';
import { Banner } from '@ui/Banner';
import TasksDrawerContent from './TasksDrawerContent';
import ToolDetails from './ToolDetails';
import { actionableTasks } from '../utils/taskClassification';
import { CitationClickContext, findCitedTask, parseCitationHref } from '../utils/citationLinks';

const taskKey = (task) => task?.id ?? task?.tool_id ?? null;

/**
 * The steps behind an answer, opened from one of its citations: the list of steps with the
 * cited one open beside it — the same pair of panels the chat opens for its answers. `note`
 * explains a list opened without a step selected.
 */
const EvidenceDrawer = ({ open, loading, tasks, activeTask, note, accountId, conversationId, onClose, onSelectTask }) => {
  const [primaryWidth, setPrimaryWidth] = useState(0);
  const list = tasks || [];
  // A citation inside a step shown here — a sub-agent's answer cites its own steps — opens that step.
  const openCitation = useCallback(
    (href) => {
      const citation = parseCitationHref(href);
      const cited = citation?.rowId ? findCitedTask(citation.rowId, tasks) : null;
      if (!cited) {
        return false;
      }
      onSelectTask(cited);
      return true;
    },
    [tasks, onSelectTask]
  );
  return (
    <CitationClickContext.Provider value={openCitation}>
      <CustomDrawer
        open={open}
        onClose={onClose}
        title={`Tasks · ${actionableTasks(list).length}`}
        width='38%'
        onWidthChange={setPrimaryWidth}
        resizable={false}
        variant='modern'
      >
        <Box sx={{ color: 'var(--ds-gray-700)' }} data-testid='evidence-drawer'>
          {loading ? (
            <ConversationLoader label='Loading the steps' />
          ) : (
            <>
              {note && <Banner tone='info' surface='section' message={note} />}
              <TasksDrawerContent
                tasks={list}
                accountId={accountId}
                conversationId={conversationId}
                activeTaskKey={taskKey(activeTask)}
                onOpenToolDetails={onSelectTask}
              />
            </>
          )}
        </Box>
      </CustomDrawer>
      <SecondaryDrawer
        open={open && !loading && Boolean(activeTask)}
        onClose={() => onSelectTask(null)}
        title='Tool Details'
        rightOffset={primaryWidth}
        defaultWidth='45%'
        variant='modern'
      >
        {activeTask && <ToolDetails toolCall={activeTask} accountId={accountId} conversationId={conversationId} />}
      </SecondaryDrawer>
    </CitationClickContext.Provider>
  );
};

EvidenceDrawer.propTypes = {
  open: PropTypes.bool.isRequired,
  loading: PropTypes.bool,
  tasks: PropTypes.array,
  activeTask: PropTypes.object,
  note: PropTypes.string,
  accountId: PropTypes.string,
  conversationId: PropTypes.string,
  onClose: PropTypes.func.isRequired,
  onSelectTask: PropTypes.func.isRequired,
};

export default EvidenceDrawer;
