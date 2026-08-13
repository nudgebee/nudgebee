import React from 'react';
import { Box } from '@mui/material';
import { useSortable } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import DragIndicatorIcon from '@mui/icons-material/DragIndicator';
import { Button } from '@ui/Button';
import Tooltip from '@ui/Tooltip';
import SafeIcon from '@shared/icons/SafeIcon';
import { DeleteIconRed as deleteIcon } from '@assets';
import { ds } from '@utils/colors';
import type { AccountOption, Panel } from '@api1/dashboards';
import DashboardPanel from './DashboardPanel';
import { EXPORT_HIDE_ATTR } from './panelImage';
import { panelMinHeight, panelSpan } from './panelDefaults';
import type { VariableValues } from './templating';

interface Props {
  panel: Panel;
  accounts: AccountOption[];
  variables: VariableValues;
  startTime: number;
  endTime: number;
  refreshToken: number;
  onEdit: () => void;
  onDelete: () => void;
  /** Begins a width drag. Owned by the view so all panels share one gesture. */
  onResizeStart: (event: React.MouseEvent) => void;
  /** True while THIS panel is the one being resized. */
  resizing: boolean;
}

/** A dashboard panel with its edit-mode affordances: drag to reorder, drag the right edge to resize, delete. */
const SortablePanel: React.FC<Props> = ({
  panel,
  accounts,
  variables,
  startTime,
  endTime,
  refreshToken,
  onEdit,
  onDelete,
  onResizeStart,
  resizing,
}) => {
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, transform, transition, isDragging } = useSortable({
    id: panel.id,
    /*
     * A resize starts on the same panel a sort would. Without this the pointer
     * moving away from the resize handle also drags the panel out of the grid.
     */
    disabled: resizing,
  });

  return (
    <Box
      ref={setNodeRef}
      data-testid={`sortable-panel-${panel.id}`}
      /*
       * The whole panel opens the editor — except its own interactive controls.
       * A table panel renders pagination and a Rows selector inside the body;
       * clicking one should drive that control, not open the editor. Same guard
       * CustomTable uses for its row-expand toggle.
       */
      onClick={(event) => {
        if (
          (event.target as HTMLElement).closest(
            'button, a, input, select, textarea, [role="button"], [role="link"], [role="menuitem"], [role="option"], [role="combobox"]'
          )
        ) {
          return;
        }
        onEdit();
      }}
      style={{
        /*
         * Translate, not Transform: `CSS.Transform` also writes a scale, which stretches a 3/12 panel to the
         * footprint of the 12/12 one it is moving past.
         */
        transform: CSS.Translate.toString(transform),
        transition,
      }}
      sx={{
        gridColumn: `span ${panelSpan(panel)}`,
        minHeight: panelMinHeight(panel),
        position: 'relative',
        cursor: 'pointer',
        // Lifts the panel being dragged over the ones reflowing beneath it.
        zIndex: isDragging ? 2 : 'auto',
        // The original stays in place as a ghost; the DragOverlay is what
        // follows the cursor.
        opacity: isDragging ? 0.4 : 1,
        outline: resizing ? `2px solid ${ds.blue[500]}` : 'none',
        outlineOffset: '-2px',
        borderRadius: '8px',
        /*
         * Charts own their pointer events — a Chart.js canvas swallows the mousemove a drag needs, and
         * tooltips chase a cursor that is dragging rather than pointing.
         */
        '& [data-panel-body]': { pointerEvents: isDragging || resizing ? 'none' : 'auto' },
      }}
    >
      <DashboardPanel
        panel={panel}
        accounts={accounts}
        variables={variables}
        startTime={startTime}
        endTime={endTime}
        refreshToken={refreshToken}
        editing
        actions={
          // Excluded from an exported image, like the panel's own menu: an affordance nobody can press reads
          // as an artefact in a PNG.
          <Box
            component='span'
            {...{ [EXPORT_HIDE_ATTR]: 'true' }}
            onClick={(event) => event.stopPropagation()}
            sx={{ display: 'inline-flex', alignItems: 'center', gap: 0.25 }}
          >
            <Tooltip title='Delete panel'>
              <Button
                tone='ghost'
                composition='icon-only'
                aria-label={`Delete ${panel.title || 'panel'}`}
                icon={<SafeIcon src={deleteIcon} alt='delete' width={15} height={15} />}
                onClick={onDelete}
                id={`delete-panel-${panel.id}`}
                data-testid={`delete-panel-${panel.id}`}
              />
            </Tooltip>
            {/*
             * A div rather than a Button: dnd-kit's listeners must land on the
             * activator node itself, and the keyboard sensor needs the node to
             * be focusable and to carry its own `attributes` (role, tabIndex,
             * aria-describedby) — which a design-system Button would swallow.
             */}
            <Tooltip title='Drag to reorder'>
              <Box
                ref={setActivatorNodeRef}
                {...attributes}
                {...listeners}
                data-testid={`drag-panel-${panel.id}`}
                sx={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  color: ds.gray[400],
                  cursor: 'grab',
                  borderRadius: 'var(--ds-radius-sm)',
                  p: '2px',
                  touchAction: 'none',
                  '&:active': { cursor: 'grabbing' },
                  '&:hover': { color: ds.gray[600], background: ds.gray[100] },
                  '&:focus-visible': { outline: `2px solid ${ds.blue[500]}`, outlineOffset: '1px' },
                }}
              >
                <DragIndicatorIcon sx={{ fontSize: 16 }} />
              </Box>
            </Tooltip>
          </Box>
        }
      />

      {/*
       * Sits on the panel's right edge, over the border. Width is the only
       * dimension that is dragged — `h` is not editable yet — so there is no
       * bottom or corner handle to pair it with.
       */}
      <Box
        onMouseDown={onResizeStart}
        // A resize ends with a mouseup on this element, which the browser then
        // reports as a click — without this every resize also opened the editor.
        onClick={(event) => event.stopPropagation()}
        aria-hidden
        {...{ [EXPORT_HIDE_ATTR]: 'true' }}
        data-testid={`resize-panel-${panel.id}`}
        sx={{
          position: 'absolute',
          top: 0,
          right: -1,
          width: '10px',
          height: '100%',
          cursor: 'col-resize',
          zIndex: 3,
          '&::after': {
            content: '""',
            position: 'absolute',
            top: '50%',
            right: '3px',
            transform: 'translateY(-50%)',
            width: '3px',
            height: '32px',
            borderRadius: '2px',
            background: ds.gray[300],
            transition: 'background 120ms ease',
          },
          '&:hover::after': { background: ds.blue[500] },
        }}
      />
    </Box>
  );
};

export default SortablePanel;
