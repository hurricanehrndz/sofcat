# Security policy

SofCat is pre-beta. Only the latest release gets security fixes.

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub:
[Report a vulnerability](https://github.com/hurricanehrndz/sofcat/security/advisories/new).
Don't open a public issue for a security problem.

Include what you found, how to reproduce it, and the version or commit you
tested. I'll acknowledge the report within a week and keep you posted on the
fix. Once a fixed release is out, the advisory is published with credit to
you unless you'd rather stay anonymous.

## Scope

SofCat runs as SYSTEM and installs software, so these matter most:

- the service and its local named pipe (`pkg/service`)
- what the agent downloads and runs from a repo (`pkg/download`, `pkg/installer`)
- the data directory and its ACL (`C:\ProgramData\SofCat`, see
  [docs/data-directory.md](docs/data-directory.md))
- the self-service app and the branding it loads (`ui/`)
