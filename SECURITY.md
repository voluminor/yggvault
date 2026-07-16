# Security policy

## Supported versions

Security support covers the latest release and the current default branch. Please update to the most recent version
before filing a report when that is practical.

## Reporting a vulnerability

Email `git@sunsung.fun` with the subject `SECURITY: <short summary>`.

Do not open a public issue, discussion, or pull request for a suspected vulnerability.

Please include:

- The affected version, commit, or deployment date.
- The relevant configuration, especially exposed web, Yggdrasil, metrics, storage, source, or brother RPC settings.
- The affected route, API, command, file format, or protocol path.
- The impact and who can trigger it.
- Clear reproduction steps.
- A minimal proof of concept when it is safe to share.
- Logs, stack traces, or packet/request samples with secrets removed.

We will:

- Acknowledge the report within 72 hours.
- Provide an initial assessment or mitigation plan within 14 days.
- Aim to fix or provide a mitigation within 90 days. Complex issues may take longer.

## Disclosure

Please keep the report private until a fix or mitigation is available. We will coordinate public disclosure and credit
you unless you ask otherwise.

## Out of scope

The following are usually out of scope unless they demonstrate a concrete, exploitable weakness in this project:

- Social engineering or physical attacks.
- Pure denial-of-service, spam, or resource-exhaustion claims without an actionable fix.
- Automated scanner output without confirmed exploitability.
- Issues that only affect unsupported versions.
- Findings that require non-default, explicitly unsafe configuration.
- Vulnerabilities in third-party dependencies that should be reported upstream.

## Safe harbor

If you follow this policy and act in good faith, we will not pursue legal action or intentionally block your research.
Stay within the minimum testing needed to prove the issue, avoid accessing data that is not yours, and stop if you
believe testing could harm another system or user.

## Security updates

Security fixes are shipped as patch releases when applicable and are noted in release notes or changelog entries.
