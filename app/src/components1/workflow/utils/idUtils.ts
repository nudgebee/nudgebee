import type { Node } from 'reactflow';

/**
 * Generate a unique node ID from a base name, suffixing `-1`, `-2`, ... if needed
 * to avoid collisions with `existingNodes`.
 */
export const generateUniqueId = (baseName: string, existingNodes: Node[] = []): string => {
  const existingIds = new Set(existingNodes.map((node) => node.id));

  if (!existingIds.has(baseName)) {
    return baseName;
  }

  let counter = 1;
  let uniqueId = `${baseName}-${counter}`;
  while (existingIds.has(uniqueId)) {
    counter++;
    uniqueId = `${baseName}-${counter}`;
  }
  return uniqueId;
};
