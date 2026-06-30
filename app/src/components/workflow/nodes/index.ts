import TriggerNode from './TriggerNode';
import ActionNode from './ActionNode';
import SwitchNode from './SwitchNode';
import BaseNode from './BaseNode';

export const nodeTypes = {
  trigger: TriggerNode,
  action: ActionNode,
  switch: SwitchNode,
};

export { TriggerNode, ActionNode, SwitchNode, BaseNode };
