# Security Policy

Lanesh executes commands and moves files on the machines that join a lane, so security reports are taken seriously.

## Reporting a vulnerability

Please report vulnerabilities privately through [GitHub private vulnerability reporting](https://github.com/jtsang4/lanesh/security/advisories/new). Do not open a public issue.

Include:

- the affected version or commit
- steps to reproduce, or a proof of concept
- the impact you believe it has

You should receive an initial response within a few days. Once a fix is available, a security advisory is published together with the release.

## Supported versions

Until 1.0, only the latest release receives security fixes.

## Security model

The threat model and trust boundaries are described in the [design document](docs/design.md#15-安全). In short:

- An access key grants full control over every machine in its lane that allows execution.
- A lane is a trust boundary: only put machines that fully trust each other in the same lane.
- The server sees all traffic in plaintext and must be self-hosted in a trusted environment.
