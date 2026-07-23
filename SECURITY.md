# Security Policy

This document describes security reporting, support scope, trust boundaries, and hardening guidance for `dbgov-cli`.

## Supported Versions

Security fixes target the latest released version. Older versions may receive critical fixes at maintainer discretion. Users should upgrade to the newest release when security fixes are published.

## Reporting a Vulnerability

Please report vulnerabilities privately through GitHub Security Advisories:

<https://github.com/JiangHe12/dbgov-cli/security/advisories/new>

Do not disclose exploitable details in public issues, discussions, social media, or chat rooms before a fix is available.

Include, when possible:

- affected version and installation method;
- operating system and architecture;
- impact and attack scenario;
- reproduction steps or proof of concept;
- relevant backend or context configuration;
- suggested mitigation, if known.

## Response Targets

| Stage | Target |
|---|---|
| Initial response | within 5 business days |
| Confirm or reject | within 30 days |
| Fix plan | after confirmation |
| Public disclosure | coordinated after a fixed release |

## Trust Boundary

`dbgov-cli` is a local CLI. It trusts the current OS user account, owner-only files under `~/.dbgov`, signed release assets from the canonical GitHub repository, and the binary manifest bound into the official npm package by provenance. It does not trust backend responses, local input files, npm mirror content, external validators, or user-provided URLs.

## Sensitive Data

- Prefer keychain or encrypted-file credential storage for production.
- Do not commit context files, tokens, passwords, audit logs, backups, or snapshots.
- Audit and backup files may contain operational metadata. Protect and rotate them according to your retention policy.

## Governance Safety

dbgov-cli enforces R0-R3 write governance, records denied and failed attempts, captures schema snapshots before schema mutations, and treats MySQL and PostgreSQL rollback as structure-only and potentially lossy.

AI agents and automation must not auto-fill `--ticket`, `--allow-*`, or high-risk `--yes`. Missing authorization must be surfaced to the human operator or change system.

## Supply Chain

Release artifacts are built and signed by GitHub Actions. Before GitHub Release and npm publication, the workflow verifies `checksums.txt` and all six binary signatures against this repository's exact `release.yml` identity, release ref, and GitHub Actions OIDC issuer. The npm package embeds those six verified digests in `package.json`, covered by npm provenance. The installer trusts only that package-bound manifest; mirrors can supply bytes but cannot replace verification data. There is no verification bypass, and a failed install leaves the previous binary unchanged.

## Hardening Checklist

- Use protected contexts for production.
- Configure ticket patterns or validators where available.
- Use RBAC roles for shared contexts.
- Use secure credential backends.
- Archive audit logs to controlled storage.
- Keep the CLI updated.
- Run production automation under the intended local OS account; audit identity is the trusted `username@hostname`, while change tickets remain human-supplied.
