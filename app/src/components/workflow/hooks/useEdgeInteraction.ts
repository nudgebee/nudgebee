import { useState, useEffect, useCallback } from 'react';
import { getSmoothStepPath, useReactFlow, type Position } from 'reactflow';

interface UseEdgeInteractionParams {
  id: string;
  sourceX: number;
  sourceY: number;
  targetX: number;
  targetY: number;
  sourcePosition: Position;
  targetPosition: Position;
}

export function useEdgeInteraction({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition }: UseEdgeInteractionParams) {
  const { setEdges } = useReactFlow();
  const [isHovered, setIsHovered] = useState(false);
  const [isEditorMode, setIsEditorMode] = useState(true);

  useEffect(() => {
    const hasEditorMode = document.querySelector('.editor-mode') !== null;
    setIsEditorMode(hasEditorMode);
  }, []);

  const [edgePath, labelX, labelY] = getSmoothStepPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
    borderRadius: 18,
  });

  const onEdgeClick = useCallback(
    (evt: React.MouseEvent) => {
      evt.stopPropagation();
      setEdges((edges) => edges.filter((edge) => edge.id !== id));
    },
    [id, setEdges]
  );

  const hoverHandlers = {
    onMouseEnter: () => setIsHovered(true),
    onMouseLeave: () => setIsHovered(false),
  };

  return {
    edgePath,
    labelX,
    labelY,
    isHovered,
    isEditorMode,
    onEdgeClick,
    hoverHandlers,
  };
}
