# Code signing policy

Free code signing provided by [SignPath.io](https://signpath.io), certificate by
[SignPath Foundation](https://signpath.org).

## What is signed

Only the Windows build of the HYPHA agent, `hypha-agent-windows-amd64.exe`, built from this
repository. No third-party binary is signed with this certificate.

- The binary is built by GitHub Actions ([`.github/workflows/agent.yml`](.github/workflows/agent.yml))
  from a version tag `vX.Y.Z` of this repository, on GitHub-hosted runners, with a pinned Go
  version and `-trimpath`.
- Its Windows version resource carries the product name `HYPHA agent` and the product version of
  the tag.
- The unsigned artifact is submitted to SignPath from the same workflow run; SignPath verifies
  its origin before signing.
- Every signing request is approved manually by an approver.

## Team roles

- Committers and reviewers: [s33d-fr](https://github.com/s33d-fr)
- Approvers: [s33d-fr](https://github.com/s33d-fr)

Changes proposed by people outside the team (pull requests) are reviewed by a committer before
they are accepted. All team members use multi-factor authentication on GitHub and on SignPath.

## Privacy policy

This program will not transfer any information to other networked systems unless specifically
requested by the user or the person installing or operating it.

In detail:

- The agent sends the data it collects (metrics, logs, traces, inventory, check results) only to
  the HYPHA server chosen by the person who installs it: the `url` setting of `hypha.yaml`
  (`HYPHA_URL` at installation). It is either a self-hosted HYPHA server or S33D's hosted service,
  `https://hypha.s33d.fr`, which requires an ingestion key created by the user on that service.
- Optional features (cloud and database connectors, SNMP devices, synthetic tests, remote actions)
  only contact the systems the operator configures, and are off until configured.
- Automatic updates are off by default (`auto_update: false`). When enabled, files are downloaded
  from the same server and their ed25519 signature is checked before installation.
- The hosted service processes data according to its privacy policy:
  <https://hypha.s33d.fr/en/privacy/> (French: <https://hypha.s33d.fr/confidentialite/>).

## Installation and removal

Installation and uninstallation instructions:
<https://hypha.s33d.fr/en/docs/agent-windows/> (the installer script supports `-Uninstall`).

## Reporting

Security issues: see [SECURITY.md](SECURITY.md). If you believe a file signed with this
certificate violates this policy, write to [contact@s33d.fr](mailto:contact@s33d.fr) and to
SignPath Foundation (<https://signpath.org>).
