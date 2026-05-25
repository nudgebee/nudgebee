import fs from 'fs';
import path from 'path';

export default function loadBrandingFile() {
  const filePath = process.env.TENANT_BRANDING_FILE || 'branding/default/theme.json';
  if (!filePath) return null;

  try {
    const resolvedPath = filePath.startsWith('/') ? filePath : path.join(process.cwd(), 'public', filePath);
    const raw = fs.readFileSync(resolvedPath, 'utf-8');
    return JSON.parse(raw);
  } catch (err) {
    console.error('Failed to load branding file:', err.message);
    return null;
  }
}
