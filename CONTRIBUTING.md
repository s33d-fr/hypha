# Contributing to HYPHA

Thank you for your interest. Issues and pull requests are welcome, in English or in French.

## Before you start

- Security problems: do not open an issue, see [SECURITY.md](SECURITY.md).
- For a large change, open an issue first so we can agree on the approach.
- Keep each pull request focused on one change, and follow the style of the surrounding code.

## Tests

```bash
# agent (Go 1.26)
cd agent && go vet ./... && go test ./...
# server (Python 3.14)
cd server && python -m pip install -r requirements.txt pytest && python -m pytest -q
```

## Review and publication

Every pull request is reviewed by a maintainer (GitHub account `s33d-fr`) before it is accepted.
This repository is published from S33D's internal repository: an accepted change is applied there
with its author credited, then appears here with the next publication.

## License of contributions

By contributing, you agree that your contribution is licensed under the license of the directory
it changes: Apache License 2.0 for `agent/`, GNU AGPL v3 for everything else.
