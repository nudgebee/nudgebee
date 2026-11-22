// Knowledge-base (skill) name rules. Extracted from KnowledgeBaseFormModal so
// they can be unit-tested without rendering the form. Hyphens are allowed so a
// hand-written skill can use the same names as Agent Skills (`pdf-processing`).

// Returns the first rule a trimmed name breaks, or '' when it is valid.
export const getKbNameError = (trimmedName: string): string => {
  if (!trimmedName) return 'Please enter a valid name for the knowledge base';
  if (!/^[a-zA-Z]/.test(trimmedName)) return 'Name must start with a letter (a-z or A-Z)';
  if (!/^[a-zA-Z][\w-]*$/.test(trimmedName)) return 'Name can only contain letters, numbers, hyphens and underscores';
  if (trimmedName.length < 3 || trimmedName.length > 50) return 'Name must be between 3 and 50 characters';
  return '';
};
