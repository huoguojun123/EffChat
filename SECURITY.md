# Security Policy

## Supported Versions

EffChat is currently pre-release software. Security fixes are provided only for
the latest commit on the public `main` branch and the latest published
pre-release.

## Reporting a Vulnerability

Do not report security vulnerabilities through a public GitHub issue.

Use [GitHub private vulnerability reporting](https://github.com/huoguojun123/EffChat/security/advisories/new)
and include:

- the affected version or commit;
- deployment details relevant to the issue;
- reproduction steps or a minimal proof of concept;
- the expected impact;
- any suggested mitigation.

Do not include real credentials, personal data, database dumps, or production
files. Use synthetic examples and redact request logs.

You should receive an acknowledgement within 7 days. The maintainer will
validate the report, coordinate a fix and disclosure date, and credit the
reporter unless anonymity is requested.

## Deployment Responsibility

EffChat is self-hosted. Operators are responsible for strong database and JWT
secrets, TLS termination, backups, access control, and keeping the host,
containers, dependencies, and EffChat release current.
