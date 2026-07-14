/**
 * ConnectView — the AI Gateway "Connect" sub-tab.
 *
 * Self-serve onboarding: shows the caller their gateway endpoints, points them at
 * the existing API Tokens modal to mint a bearer token, and gives copy-paste setup
 * snippets for the common tools (Claude Code / OpenAI SDK / Gemini / curl).
 *
 * Purely presentational + client-side templating. The only server input is the
 * gateway's public base URL (threaded from getServerSideProps); when it's unset the
 * view shows a "not configured yet" empty state instead of broken snippets. Not an
 * EE surface — onboarding ships to everyone.
 */
import * as React from 'react';
import { Box } from '@mui/material';
import { Card } from '@ui/Card';
import { Banner } from '@ui/Banner';
import { Button } from '@ui/Button';
import { CodeBlock } from '@ui/CodeBlock';
import KeyOutlinedIcon from '@mui/icons-material/KeyOutlined';
import CustomTabs from '@shared/navigation/Tabs';
import ApiTokens from '@shared/settings/ApiTokens';

interface ConnectViewProps {
  /** The gateway's public base URL (e.g. https://gateway.example.com); empty when unconfigured. */
  gatewayUrl?: string;
}

/** Bearer placeholder — the real token is minted in the API Tokens modal, shown once there. */
const TOKEN = '<YOUR_TOKEN>';

/** Base-URL placeholder used when the gateway URL isn't configured yet, so the panel
 * still renders end-to-end with an obvious "fill this in" host. */
const PLACEHOLDER_BASE = 'https://<your-gateway-host>';

const ENDPOINTS = [
  { label: 'Anthropic', path: '/anthropic' },
  { label: 'OpenAI', path: '/openai' },
  { label: 'Gemini', path: '/genai' },
];

type SnippetId = 'claude' | 'openai' | 'gemini' | 'curl';

function buildSnippets(base: string): { id: SnippetId; text: string; language: string; code: string }[] {
  return [
    {
      id: 'claude',
      text: 'Claude Code',
      language: 'bash',
      code: `export ANTHROPIC_BASE_URL="${base}/anthropic"\nexport ANTHROPIC_AUTH_TOKEN="${TOKEN}"\nclaude`,
    },
    {
      id: 'openai',
      text: 'OpenAI SDK',
      language: 'python',
      code: `from openai import OpenAI\n\nclient = OpenAI(\n    base_url="${base}/openai",\n    api_key="${TOKEN}",\n)`,
    },
    {
      id: 'gemini',
      text: 'Gemini',
      language: 'python',
      code: `from google import genai\n\nclient = genai.Client(\n    api_key="${TOKEN}",\n    http_options={"base_url": "${base}/genai"},\n)`,
    },
    {
      id: 'curl',
      text: 'curl',
      language: 'bash',
      code:
        `curl ${base}/anthropic/v1/messages \\\n` +
        `  -H "authorization: Bearer ${TOKEN}" \\\n` +
        `  -H "anthropic-version: 2023-06-01" \\\n` +
        `  -H "content-type: application/json" \\\n` +
        `  -d '{"model":"claude-haiku-4-5-20251001","max_tokens":16,"messages":[{"role":"user","content":"ping"}]}'`,
    },
  ];
}

const sectionTitle = {
  fontSize: 'var(--ds-text-body)',
  fontWeight: 'var(--ds-font-weight-semibold)',
  color: 'var(--ds-gray-800)',
} as const;

const sectionHint = {
  fontSize: 'var(--ds-text-small)',
  color: 'var(--ds-gray-600)',
} as const;

function Section({ title, hint, children }: { title: string; hint?: string; children: React.ReactNode }) {
  return (
    <Card sx={{ p: 'var(--ds-space-4)' }}>
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
          <Box sx={sectionTitle}>{title}</Box>
          {hint && <Box sx={sectionHint}>{hint}</Box>}
        </Box>
        {children}
      </Box>
    </Card>
  );
}

export function ConnectView({ gatewayUrl }: ConnectViewProps) {
  const [tokenOpen, setTokenOpen] = React.useState(false);
  const [snippet, setSnippet] = React.useState<SnippetId>('claude');

  const configuredBase = (gatewayUrl || '').replace(/\/+$/, '');
  const configured = configuredBase !== '';
  // When the admin hasn't set the gateway URL yet, still render the full panel with a
  // clearly-marked placeholder host so the endpoints, token flow, and snippets are all
  // visible — just with a banner telling the user (or admin) to swap in the real host.
  const base = configured ? configuredBase : PLACEHOLDER_BASE;

  const snippets = buildSnippets(base);
  const active = snippets.find((s) => s.id === snippet) ?? snippets[0];
  const snippetTabs = snippets.map((s) => ({ value: s.id, text: s.text }));

  return (
    <Box id='gateway-connect' sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)' }}>
      {!configured && (
        <Banner
          tone='warning'
          title='Gateway URL not configured'
          message='The endpoints and snippets below use a placeholder host. Replace <your-gateway-host> with your gateway’s address, or ask your administrator to configure it.'
        />
      )}

      <Section title='Your endpoints' hint='Point your tool at the endpoint for the provider you want to use.'>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-2)' }}>
          {ENDPOINTS.map((e) => (
            <CodeBlock key={e.label} title={e.label} code={`${base}${e.path}`} wrap />
          ))}
        </Box>
      </Section>

      <Section
        title='Your token'
        hint='Use an NB API token as the bearer token in the snippets below. Keep it secret — it grants access to your organization’s providers through the gateway.'
      >
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', flexWrap: 'wrap' }}>
          <Button
            id='gateway-connect-generate-token-btn'
            tone='primary'
            icon={<KeyOutlinedIcon sx={{ fontSize: 16 }} />}
            onClick={() => setTokenOpen(true)}
          >
            Generate token
          </Button>
          <Button id='gateway-connect-manage-tokens-btn' tone='ghost' onClick={() => setTokenOpen(true)}>
            Manage tokens
          </Button>
        </Box>
      </Section>

      <Section title='Setup' hint='Copy the snippet for your tool and replace the token placeholder with the one you generated.'>
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-3)' }}>
          <CustomTabs
            options={{ tabOptions: snippetTabs }}
            value={snippet}
            onChange={(next: string) => setSnippet(next as SnippetId)}
            behavior='filter'
            ariaLabel='Setup snippets'
          />
          <CodeBlock id={`gateway-connect-snippet-${active.id}`} code={active.code} language={active.language} tone='dark' wrap />
          <Banner
            tone='info'
            message='The verify request uses a low-cost model and a tiny prompt — a 200 response confirms your endpoint and token work.'
          />
        </Box>
      </Section>

      <ApiTokens open={tokenOpen} title='API Tokens' onClose={() => setTokenOpen(false)} />
    </Box>
  );
}

export default ConnectView;
