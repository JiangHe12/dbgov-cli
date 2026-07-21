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

`dbgov-cli` is a local CLI. It trusts the current OS user account, owner-only files under `~/.dbgov`, and binaries/checksums published by the canonical GitHub repository. It does not trust backend responses, local input files, npm mirrors, external validators, or user-provided URLs.

## Sensitive Data

- Prefer keychain or encrypted-file credential storage for production.
- Do not commit context files, tokens, passwords, audit logs, backups, or snapshots.
- Audit and backup files may contain operational metadata. Protect and rotate them according to your retention policy.

## Governance Safety

dbgov-cli enforces R0-R3 write governance, records denied and failed attempts, captures schema snapshots before schema mutations, and treats MySQL and PostgreSQL rollback as structure-only and potentially lossy.

AI agents and automation must not auto-fill `--ticket`, `--allow-*`, or high-risk `--yes`. Missing authorization must be surfaced to the human operator or change system.

## Supply Chain

Release artifacts are built by GitHub Actions. npm installation downloads platform binaries from GitHub Releases and verifies checksums when supported by the package installer. Prefer official releases and avoid running unverified binaries.

## Hardening Checklist

- Use protected contexts for production.
- Configure ticket patterns or validators where available.
- Use RBAC roles for shared contexts.
- Use secure credential backends.
- Archive audit logs to controlled storage.
- Keep the CLI updated.
- Run production automation under the intended local OS account; audit identity is the trusted `username@hostname`, while change tickets remain human-supplied.
