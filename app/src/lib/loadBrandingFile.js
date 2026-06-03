import fs from 'fs';
import path from 'path';

// Module-level cache: the branding file is read at most once per process.
// `loaded` distinguishes "not yet attempted" from "attempted and missing"
// (in which case `value` is the legitimate null return).
let _loaded = false;
let _value = null;

const DEFAULT_BRANDING_PATH = 'branding/default/theme.json';

export default function loadBrandingFile() {
  if (_loaded) return _value;
  _loaded = true;

  const filePath = process.env.TENANT_BRANDING_FILE || DEFAULT_BRANDING_PATH;
  if (!filePath) return _value;

  try {
    const resolvedPath = filePath.startsWith('/') ? filePath : path.join(process.cwd(), 'public', filePath);
    const raw = fs.readFileSync(resolvedPath, 'utf-8');
    _value = JSON.parse(raw);
    return _value;
  } catch (err) {
    // ENOENT on the default path is expected — the default theme.json was
    // intentionally removed. Only surface that case at info level (once)
    // and stay silent on subsequent calls. Operator-configured paths and
    // any non-ENOENT error (parse failure, permission, etc.) still log
    // loud because those signal real misconfiguration.
    const isDefault = filePath === DEFAULT_BRANDING_PATH;
    if (err && err.code === 'ENOENT' && isDefault) {
      console.info('No branding file found at default path; using fallbacks.');
    } else {
      console.error('Failed to load branding file:', err.message);
    }
    return _value;
  }
}
