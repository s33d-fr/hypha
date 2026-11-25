# Code signing policy

The Windows build of the HYPHA agent is signed by S33D with an organization-validated (OV) code
signing certificate issued by [Certum](https://www.certum.eu) (Asseco Data Systems S.A.).

| Certificate | |
|---|---|
| Subject | `CN=S33D, O=S33D, L=Faumont, ST=Hauts-de-France, C=FR` |
| Issuer | `Certum Code Signing 2021 CA` |
| Serial number | `6E5FC59DF514928075C5971180D7443B` |
| SHA-1 thumbprint | `58608C1836F7C4727F5CFB787E70BEE80BC74E37` |
| Valid | 2026-11-25 to 2027-11-25 |

The private key (RSA 4096) was generated in Certum's cloud signing service (SimplySign) and cannot
be exported. Each signature carries an RFC 3161 timestamp from Certum, so it stays valid after the
certificate expires.

## What is signed

Only software published by S33D. For HYPHA: the Windows build of the agent,
`hypha-agent-windows-amd64.exe`. No third-party binary is signed with this certificate.

- Release binaries are built by S33D from a version tag `vX.Y.Z` of this repository with
  [`agent/packaging/build.sh`](agent/packaging/build.sh) (`-trimpath`, version set at link time).
  The Windows executable is signed right after it is built, before the checksums are computed.
- `SHA256SUMS` lists every file of a release (binaries, `.deb`, `.rpm`, `version.txt`) and is
  signed with ed25519 (`SHA256SUMS.sig`); the public key is
  [`agent/internal/update/release_pubkey.txt`](agent/internal/update/release_pubkey.txt), the
  same key the agent uses to check automatic updates.
- GitHub Actions ([`.github/workflows/agent.yml`](.github/workflows/agent.yml)) runs the tests and
  builds every push; its artifacts are not signed and are not release files.
- Release files are published on the [Releases](https://github.com/s33d-fr/hypha/releases) page
  of this repository and on `https://hypha.s33d.fr/install/`, with the same checksums.

## Verifying a download

- Windows (PowerShell): `Get-AuthenticodeSignature .\hypha-agent-windows-amd64.exe` must report
  `Valid` with the subject above.
- Any system: `sha256sum -c SHA256SUMS`, then check `SHA256SUMS.sig` with the public key above.

## Team roles

- Committers and reviewers: [s33d-fr](https://github.com/s33d-fr)
- Approvers: [s33d-fr](https://github.com/s33d-fr)

Changes proposed by people outside the team (pull requests) are reviewed by a committer before
they are accepted. All team members use multi-factor authentication on GitHub. Signing runs only
on S33D's build host, which holds the credentials of S33D's Certum account.

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
Certum (<https://www.certum.eu>).
