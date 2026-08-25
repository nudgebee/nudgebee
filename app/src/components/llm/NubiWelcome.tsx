import React, { useEffect, useState } from 'react';
import { Typography } from '@mui/material';
import { ds } from '@utils/colors';

export const buildNubiWelcomeTemplates = (name: string): string[] => [
  `Hey, I'm ${name} — how can I help you today?`,
  `Hi there! I'm ${name}. What can I help you troubleshoot?`,
  `${name} here. Ask me anything about your clusters and workloads.`,
  `Hello! I'm ${name}, your troubleshooting copilot. Where should we start?`,
  `Hey! I'm ${name}. Got an issue? Let's dig into it together.`,
  `Hi, I'm ${name}. What can I help you figure out?`,
];

export const useNubiWelcomeMessage = (assistantName?: string): string => {
  // Pick after mount, not during render, to avoid an SSR/hydration mismatch.
  const [message, setMessage] = useState('');
  useEffect(() => {
    const templates = buildNubiWelcomeTemplates(assistantName || 'Nubi');
    setMessage(templates[Math.floor(Math.random() * templates.length)]);
  }, [assistantName]);
  return message;
};

interface NubiWelcomeProps {
  assistantName?: string;
}

const NubiWelcome: React.FC<NubiWelcomeProps> = ({ assistantName }) => {
  const message = useNubiWelcomeMessage(assistantName);

  if (!message) {
    return null;
  }

  return (
    <Typography
      className='animated-box'
      style={{ animationDelay: '0.2s' }}
      sx={{
        mt: ds.space[3],
        fontSize: 'var(--ds-text-body-lg)',
        fontWeight: 'var(--ds-font-weight-regular)',
        color: ds.gray[600],
        fontFamily: ds.font.sans,
        lineHeight: 1.5,
      }}
    >
      {message}
    </Typography>
  );
};

export default NubiWelcome;
