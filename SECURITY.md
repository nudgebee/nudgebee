# Security Policy

The Nudgebee team takes security seriously. We appreciate responsible disclosure from the community.

## Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub issues, pull requests, or discussions.**

Instead, report them privately via either of:

- **Email**: <security@nudgebee.com>
- **GitHub private vulnerability reporting**: <https://github.com/nudgebee/nudgebee/security/advisories/new>

Include in your report:

- A description of the vulnerability and its potential impact
- Steps to reproduce (proof-of-concept code or commands welcome)
- Affected version(s), commit SHAs, or deployment configuration
- Any suggested mitigations or fixes

You should receive an acknowledgement within **2 business days**. We will keep you informed of progress toward a fix and disclosure.

## Disclosure Process

1. Reporter submits via one of the channels above.
2. We confirm receipt within 2 business days.
3. We investigate, validate, and develop a fix on a private branch.
4. We coordinate a release window with the reporter.
5. Fix is shipped to supported versions; a security advisory is published; the reporter is credited (unless they prefer to remain anonymous).

## Supported Versions

Security fixes are applied to the latest minor release on the `main` branch. Older versions are best-effort.

| Version | Supported |
| ------- | --------- |
| latest `main` | ✅ |
| previous minor | ✅ (critical only) |
| older | ❌ |

## Scope

In scope:

- Source code in this repository (all services in the monorepo)
- The default Helm chart and Kubernetes manifests under `deploy/`
- Container images built from this repository

Out of scope:

- Third-party dependencies — please report those upstream (we will help coordinate where appropriate)
- Self-hosted deployments configured contrary to documented guidance
- Attacks requiring a malicious cluster operator with cluster-admin privileges
- Social engineering, physical attacks, and denial-of-service via volumetric flooding

## Safe Harbor

We will not pursue legal action against researchers who:

- Make a good-faith effort to comply with this policy
- Do not access, modify, or destroy data beyond what is necessary to demonstrate the vulnerability
- Give us reasonable time to respond before any public disclosure

Thank you for helping keep Nudgebee and its users safe.

## Architecture references

- [Authentication and NetworkPolicy](docs/auth-and-networkpolicy.md) — the
  trust model backend services rely on (BFF as the auth boundary,
  shared internal tokens, NetworkPolicy expectations) and what
  contributors need to do when adding a new internal endpoint or
  caller.
