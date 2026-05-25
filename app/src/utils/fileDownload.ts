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
