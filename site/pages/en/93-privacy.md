---
id: privacy
kind: page
path: /en/privacy/
title: HYPHA privacy policy
updated: 2026-11-19
description: How S33D processes personal data in the HYPHA service: account and billing data, customers' monitoring data, retention periods, processors, hosting in France, data subject rights.
lead: HYPHA is hosted by S33D on its own infrastructure, in France. This page says what data we process, why, for how long, with whom, and how to exercise your rights. Version of 19 November 2026. This English translation is provided for convenience; the French version prevails.
---
## 1. Who processes your data

**S33D**, a French SARL with a share capital of €1,000, Douai Trade and Companies Register 131 908 329, 6 Résidence Beauséjour, 59310 Faumont, France. Contact for any question about your data: [contact@s33d.fr](mailto:contact@s33d.fr).

S33D acts in two capacities:

| Data | S33D's role |
|---|---|
| Accounts, members, billing, support, security and measurement of the site and the interface | **controller** |
| Monitoring data sent by customers (metrics, logs, traces, inventory, events) | **processor** for the customer, who is the controller |

When self-hosting (Libre plan), S33D receives no data: everything stays on the customer's server.

## 2. Data for which S33D is the controller

| Data | Purpose | Legal basis | Retention |
|---|---|---|---|
| Account: identifier, email address, name, last sign-in date | create and secure access, link to workspaces | performance of the contract | as long as the account exists; erasure on request |
| Workspaces: membership, role, invitations | manage rights within a workspace | performance of the contract | as long as the workspace exists |
| Session: `hypha_session` cookie, IP address and browser of the session | keep you signed in | performance of the contract | 7 days |
| Audit log: author, action, object, date, IP address | security, proof of changes | legitimate interest | 13 months |
| Billing: company name, billing email, country, VAT number, subscription, usage, invoices | bill paid plans | performance of the contract, legal obligation | 10 years for accounting records |
| Exchanges with support | answer requests | performance of the contract, legitimate interest | 3 years after the last exchange |
| Server technical logs: IP address, date, requested page, browser | security and proper operation of the service | legitimate interest, legal obligation | 1 year at most |
| Interface measurement: page views, load times, JavaScript errors, without address parameters | fix slowness and errors in the interface | legitimate interest | 30 days |

The S33D account is used to sign in to several S33D services. If you choose to sign in with Google, Microsoft, Apple, Facebook or GitHub, S33D only receives your identifier, verified email address and name from that provider; it never has access to your password.

S33D sells no data and makes no advertising use of it.

## 3. Monitoring data

The data the customer sends to the Service may contain personal data: user names or IP addresses in logs, email addresses, machine names, trace attributes, addresses of pages viewed by the visitors of a site followed by RUM. For this data:

- the **customer is the controller**: it chooses what it sends, informs the people concerned and answers their requests;
- **S33D is the processor**: it only processes this data to provide the Service, according to the customer's configuration, under section 13 of the [terms of sale](/en/terms/); the Entreprise plan includes a signed data processing agreement;
- explanations and postmortems are written by S33D's AI model, on its own servers in France. Data sent to the model goes to no AI provider and is not used to train a model;
- the customer has tools to limit what it sends: masking on arrival (`secrets` rule on by default; cards, IBANs, emails, phone numbers, public IP addresses, social security numbers as chosen), exclusion filters, lines dropped by the agent before any send.

### Retention periods

The viewing period depends on the plan; data is then erased when the regular database purge runs, shown in the last column.

| Data | Découverte | Pro and Entreprise | Erased at the latest |
|---|---|---|---|
| Logs and traces | 3 days | 15 days; 30 or 90 days with the extended retention option | 16 days after receipt; with the option, 31 or 91 days |
| Full-resolution metrics | 1 day | 15 days | 16 days |
| 5-minute metrics | 30 days | 92 days | 93 days |
| One-hour metrics | not shown | 15 months | 16 months |
| Database queries (DBM) | 15 days | 15 days | 16 days |
| RUM events of monitored sites | 30 days | 30 days | 31 days |
| Processes, network connections, containers | 3 days | 3 days | 4 days |
| Check results | 30 days | 30 days | 31 days |
| Synthetic test results | 92 days | 92 days | 93 days |
| Events and alert state changes | 13 months | 13 months | 14 months |
| Hardware and software inventory | latest report of each host | latest report of each host | when the host or workspace is deleted |

Workspace settings (monitors, dashboards, channels, incidents) are kept as long as the workspace exists. At the owner's request, the workspace and its data are erased without waiting for these deadlines, subject to billing records.

## 4. Recipients and processors

Data is only accessible to the S33D staff who need it for the Service, and to workspace members according to their role. S33D uses the following processors:

| Processor | Role | Data | Location |
|---|---|---|---|
| Stripe Payments Europe, Ltd. | payment and billing of paid plans | billing identity, email, country, VAT number, payment method (entered at Stripe), amounts | Ireland; possible transfers to the United States, covered by the European Commission's standard contractual clauses |
| OVH SAS | hosting of S33D's two mail relay servers | notification and report emails (recipient address, alert content) | France |

No other processor receives data from the Service: no audience measurement tool, no advertising network, no AI provider. SMS notifications, announced as coming, will lead to an update of this list before they open.

The notification channels the customer configures (Slack, Microsoft Teams, Telegram, Discord, Google Chat, Mattermost, ntfy, PagerDuty, Opsgenie, webhooks) receive alerts on the customer's instructions; they are the customer's own providers. Likewise, cloud connectors read the accounts the customer designates, from its own agents.

S33D may disclose data to authorities requesting it within a legal framework.

## 5. Hosting and transfers

The Service and its data are hosted on S33D's own infrastructure, in France. The only possible transfers outside the European Union are Stripe's, described above, and those the customer triggers by choosing a notification channel located outside the Union.

## 6. Security

S33D encrypts exchanges (HTTPS), only keeps the fingerprint of API keys, records every change in the audit log, separates data by workspace and applies the measures described on the [Security](/en/docs/security/) page. No measure removes all risk: S33D informs the customer of any personal data breach concerning it without undue delay.

## 7. Cookies and browser storage

| Name | Type | Purpose | Duration |
|---|---|---|---|
| `hypha_session` | cookie, necessary | sign-in to the interface | 7 days |
| `hypha_oidc` | cookie, necessary | protect sign-in with the S33D account | 10 minutes |
| `hypha.lang`, `hypha.theme`, `hypha.nav`, `hypha.refresh` | local storage | chosen language, theme, menu and refresh | until you clear them |
| `hypha_rum_sid` | session storage | interface performance measurement | 30 minutes of inactivity, and closing the tab |

No advertising or social network cookie. Stripe's payment page sets its own cookies, governed by Stripe's policy.

The RUM script the customer places on its own sites sets no cookie; it keeps a session identifier in the visitor's tab (`sessionStorage`) and removes address parameters by default. The customer informs its visitors of this measurement.

## 8. Your rights

You have the right of access, rectification, erasure, restriction, portability and objection, and the right to give instructions on the fate of your data after your death. Write to [contact@s33d.fr](mailto:contact@s33d.fr); we answer within one month. Proof of identity may be requested if there is a doubt about your identity.

For monitoring data, first contact the customer using HYPHA: it is the controller. S33D forwards any request it receives to the customer.

You can lodge a complaint with the CNIL, the French data protection authority (3 place de Fontenoy, TSA 80715, 75334 Paris Cedex 07, [cnil.fr](https://www.cnil.fr)), or with the authority of your country.

## 9. Changes

This policy may change, in particular with new features. The version date is at the top of the page; important changes are announced by email to workspace owners.
