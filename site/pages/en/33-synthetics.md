---
id: synthetics
path: /en/docs/synthetics/
title: Synthetic tests
nav: Synthetic tests
section: Observe
order: 33
updated: 2026-11-19
description: HYPHA synthetic tests: HTTP, TCP, TLS, DNS, ICMP, multistep journeys, SMTP, IMAP, mail round trip and mail DNS, from S33D or from your agents.
lead: HYPHA exercises your sites, APIs and mail at regular intervals, from S33D or from inside your network.
---
## Test types

| Type | What it checks |
|---|---|
| `http` | expected status, expected text, duration, redirects, certificate expiry |
| `multistep` | a journey of several requests, with assertions and variables extracted from one step to the next |
| `tcp` | a port is open |
| `ssl` | the certificate presented (chain, name, expiry), including behind STARTTLS (SMTP, IMAP, POP3) |
| `dns` | the answer for a record (type, queried server, expected value) |
| `icmp` | ping answer |
| `smtp`, `imap` | banner, capabilities, STARTTLS or TLS depending on the port, certificate |
| `mail_roundtrip` | authenticated send, then IMAP fetch of the message; or through an external echo whose SPF and DKIM report is read |
| `mail_dns` | MX, SPF (single, final qualifier, 10 lookups at most), DMARC, DKIM for the given selectors, blocklists |

Interval: 30 seconds to 24 hours; 5 minutes at least for `mail_roundtrip` and `mail_dns`.

## Assertions

`http`, `ssl` tests and each step of a `multistep` accept assertions:

| Assertion | Example |
|---|---|
| `status` | equals 200 |
| `header` | `content-type` contains `json` |
| `body_contains` | the body contains `Order confirmed` |
| `json_path` | `$.stock` greater than 0 |
| `latency_ms` | less than 800 |

Operators: `eq`, `ne`, `lt`, `gt`, `contains`, `matches`.

### Multistep journeys

Each step can extract a value (`json_path`, header or regular expression) and reuse it later as `{{name}}` in the address, headers or body. Example: get a token, then call the API with it. The result details each step's duration, status and assertions.

## Locations

- **S33D**: the test runs from S33D's servers, towards public addresses only.
- **Your agents** (private location): the test runs from a host's network, internal addresses included. Turn on `synthetics.enabled: true` on the agent; it receives its tests through the control channel and returns the results. `max_concurrent` caps simultaneous tests (4 by default).

A test can run from several locations; the test page shows results per location, and the monitor can alert only from a given number of failing locations. The `mail_roundtrip` and `mail_dns` tests run from S33D only.

## Mail

For mail monitored end to end:

1. `smtp` on each MX (port 25) and on submission (587);
2. `imap` on 993;
3. `ssl` with STARTTLS for certificate expiry;
4. `mail_roundtrip`: a dedicated mailbox sends itself a message and fetches it; each run only deletes its own message;
5. `mail_dns` on each of your domains: SPF, DMARC, DKIM and blocklists.

A test mailbox password is returned masked by the API and never shown.

## Alerting

The **Synthetic test failing** template alerts after N consecutive failures (2 by default). Certificates collected by tests also appear in the **Certificates** page.

## What plans count

Tests run from S33D are counted per plan; tests run from your agents are never limited. See [pricing](/en/pricing/).
