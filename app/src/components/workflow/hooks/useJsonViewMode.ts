import { useState, useEffect, useCallback } from 'react';
import { useCopyToClipboard } from './useCopyToClipboard';

interface UseJsonViewModeOptions<T> {
  value: T | undefined;
  onChange: (value: T | undefined) => void;
}

export function useJsonViewMode<T>({ value, onChange }: UseJsonViewModeOptions<T>) {
  const [viewMode, setViewMode] = useState<'structured' | 'json'>('structured');
  const [jsonValue, setJsonValue] = useState('');
  const [jsonError, setJsonError] = useState('');
  const { copied, copy } = useCopyToClipboard();

  useEffect(() => {
    if (value) {
      setJsonValue(JSON.stringify(value, null, 2));
    } else {
      setJsonValue('');
    }
  }, [value]);

  const handleJsonChange = useCallback(
    (newJson: string) => {
      setJsonValue(newJson);
      if (!newJson.trim()) {
        setJsonError('');
        onChange(undefined);
        return;
      }
      try {
        const parsed = JSON.parse(newJson);
        setJsonError('');
        onChange(parsed);
      } catch (e) {
        setJsonError((e as Error).message);
      }
    },
    [onChange]
  );

  const handleCopy = useCallback(async () => {
    const textToCopy = viewMode === 'json' ? jsonValue : JSON.stringify(value || {}, null, 2);
    await copy(textToCopy);
  }, [viewMode, jsonValue, value, copy]);

  return {
    viewMode,
    setViewMode,
    jsonValue,
    jsonError,
    copied,
    handleJsonChange,
    handleCopy,
  };
}
