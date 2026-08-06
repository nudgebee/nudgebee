/**
 * Serialises a value and triggers a JSON file download.
 *
 * Pretty-printed with two spaces because the file is meant to be read and
 * hand-edited — a dashboard or panel definition someone diffs, tweaks and pastes
 * back — not just moved between machines.
 *
 * @param data - Anything JSON-serialisable
 * @param filename - Name of the file to download, with or without `.json`
 */
export const downloadJsonFile = (data: unknown, filename: string): void => {
  const name = filename.endsWith('.json') ? filename : `${filename}.json`;
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });

  const url = window.URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = name;
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
  window.URL.revokeObjectURL(url);
};

/**
 * Turns a title into a safe filename stem — lowercased, non-alphanumerics
 * folded to single hyphens. Falls back to `fallback` for a title that is all
 * punctuation, so the download is never named `.json`.
 */
export const filenameSlug = (title: string, fallback: string): string => {
  const slug = (title || '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
  return slug || fallback;
};

/**
 * Decodes a base64 string and triggers a file download
 * @param fileData - Base64 encoded file data
 * @param filename - Name of the file to download
 * @param contentType - MIME type of the file
 */
export const downloadBase64File = (fileData: string, filename: string, contentType: string): void => {
  // Decode base64 using modern approach
  const binaryString = atob(fileData);
  const bytes = Uint8Array.from(binaryString, (char) => char.charCodeAt(0));
  const blob = new Blob([bytes], { type: contentType });

  const url = window.URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
  window.URL.revokeObjectURL(url);
};
