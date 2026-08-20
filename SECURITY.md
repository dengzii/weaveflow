# Security Policy

## Supported versions

Security fixes are applied to the latest release. Users should upgrade before reporting a vulnerability when possible.

## Reporting a vulnerability

Please do not open a public issue for a security vulnerability. Use GitHub's private vulnerability reporting form:

https://github.com/lazy-banana/weaveflow/security/advisories/new

Include the affected version or commit, reproduction steps, impact, and any suggested mitigation. Remove credentials,
tokens, private graph data, and personal information from reports and attachments.

We will acknowledge reports as soon as practical, investigate privately, and coordinate a fix and disclosure timeline
with the reporter.

## Deployment guidance

Use a management token for non-local deployments, keep Docker Hub tokens in GitHub Actions secrets, and avoid exposing
the debug server directly to an untrusted network. See `scripts/README.md` for container hardening settings.
