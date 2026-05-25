export default function handler(req, res) {
  const githubAppName = process.env.NEXT_PUBLIC_GITHUB_APP_NAME || 'nudgebee';
  res.status(200).json({ githubAppName });
}
