---
id: terms
kind: page
path: /en/terms/
title: HYPHA terms of sale
updated: 2026-11-19
description: General terms of sale of S33D's hosted HYPHA service: Découverte, Pro and Entreprise plans, prices, usage-based billing, payment, cancellation, data, liability.
lead: These terms govern the use of the HYPHA service hosted by S33D on hypha.s33d.fr. Version of 19 November 2026. This English translation is provided for convenience; the French version prevails.
---
## 1. Purpose

These general terms of sale ("Terms") govern the provision of the **HYPHA** monitoring service hosted by S33D and available at hypha.s33d.fr (the "Service"), in its **Découverte**, **Pro** and **Entreprise** plans. Creating a workspace or subscribing to a plan means accepting the Terms in force.

The **Libre** plan (the software installed by the customer on its own servers) is not a service provided by S33D: it is governed by the software licences, AGPL v3 for the server and interface, Apache 2.0 for the agent, and not by these Terms.

## 2. Provider

The Service is provided by **S33D**, a French SARL with a share capital of €1,000, registered with the Douai Trade and Companies Register under number 131 908 329, EU VAT number FR16131908329, whose registered office is at 6 Résidence Beauséjour, 59310 Faumont, France. Contact: [contact@s33d.fr](mailto:contact@s33d.fr). See the [legal notice](/en/legal-notice/).

## 3. Customers

The Service is aimed at businesses, non-profits, education and research institutions, local authorities and individuals. The provisions specific to consumers (sections 10 and 16) only apply to a customer acting outside any professional activity.

## 4. Account and workspace

Access is through an S33D account. The first sign-in creates a **workspace** on the Découverte plan, whose user is its **owner**. Only the owner chooses the plan and manages billing; the owner answers for the members they invite. The customer keeps its credentials and keys confidential and revokes any compromised key.

## 5. Description of the Service

The Service collects, stores and displays the monitoring data the customer sends through its agents, connectors and applications: metrics, logs, traces, checks, events, inventory. It evaluates monitors, sends notifications, writes explanations and postmortems with S33D's artificial intelligence model, and runs, after the customer's approval, remote actions allowed by the configuration of its agents. The content of each plan is described on the [Pricing](/en/pricing/) page in force on the date of subscription.

Features announced as "in preparation" or "coming" on the Pricing page (SAML SSO, SMS notifications) are only owed once they are made available.

## 6. Prices

Prices are stated in euros **excluding VAT**. French VAT at the applicable rate (20%) is added. A business customer established in another European Union member state, whose VAT number is validated, is invoiced without VAT under the reverse-charge mechanism.

- **Découverte**: free, without a payment method. It is limited (number of hosts and devices, daily volume of logs and traces, synthetic tests, AI explanations, status pages, retention periods) as stated on the Pricing page. What exceeds a limit is refused and reported, never billed.
- **Pro**: €2 excl. VAT per host per month, without commitment or minimum; €1.50 excl. VAT per host per month on a yearly plan (€18 excl. VAT per committed host per year). Network devices and cloud resources, log and trace volume beyond the included amount, extended retention, synthetic runs beyond the included tests and SMS are billed as stated on the Pricing page.
- **Entreprise**: the Pro price, plus a fee of €250 excl. VAT per month.
- **Discounts**: non-profits, education and research institutions: −50% on Pro hosts, with proof, applied by S33D. Local authorities: Pro price.

## 7. Usage calculation

- A host, device or cloud resource is counted for each hour during which it sends data, over the billing period, not at peak.
- Log and trace volume is the volume received and stored by the Service, in decimal gigabytes. Included volumes (10 GB of logs and 5 GB of traces per host per month) are pooled across the workspace.
- The period's usage is visible at any time in **Settings › Plan and billing**, with an estimate of the invoice. The Service's records are authoritative.

## 8. Order and payment

Subscription happens in **Settings › Plan and billing**. Payment is processed by **Stripe**, by card or the means Stripe offers on the payment page; S33D never has access to the customer's bank details. The Entreprise plan can be paid by purchase order or French public administrative mandate.

- **Monthly**: the period runs from the 1st to the last day of the month (UTC). The period's usage is billed at its end and charged to the registered payment method.
- **Yearly**: the committed number of hosts is paid in advance for twelve months. Hosts beyond the commitment are billed each month at the monthly price.
- Invoices are issued by S33D and available in the payment portal (**Payment and invoices**).

If a payment fails, Stripe presents it again. If every attempt fails, the workspace returns to the Découverte limits until payment; amounts due remain payable. Any late payment by a business customer incurs penalties at three times the French statutory interest rate and the €40 fixed recovery fee (article L.441-10 of the French Commercial Code).

## 9. Term, plan changes and cancellation

- The monthly plan is **without commitment** and renews each month.
- The yearly plan commits the chosen number of hosts for twelve months.
- Moving from Pro to Entreprise takes effect immediately, the fee being billed prorated for the current month; going back from Entreprise to Pro gives a prorated credit.
- The customer cancels at any time with **Back to Découverte**. The subscription ends at the end of the current period, or at the end of the yearly commitment. The period's usage remains due; a period paid in advance is not refunded, except under section 10.
- After cancellation, the workspace continues on Découverte, with its limits and retention periods.

## 10. Consumer right of withdrawal

A consumer customer has fourteen days from subscribing to a paid plan to withdraw, without giving reasons, by writing to [contact@s33d.fr](mailto:contact@s33d.fr) (articles L.221-18 et seq. of the French Consumer Code). As the Service starts upon subscription at the customer's request, the customer pays, on withdrawal, the amount corresponding to the Service provided until their decision was communicated (article L.221-25). Amounts paid in advance beyond that amount are refunded within fourteen days.

## 11. S33D's obligations

S33D implements reasonable means to ensure the availability, security and confidentiality of the Service, which it hosts on its own infrastructure in France. The Service may be interrupted for maintenance, outside business hours where possible. Unless otherwise agreed in the Entreprise plan (support in French with guaranteed response times, under the terms agreed with the customer), S33D has an obligation of means.

Explanations, postmortems and actions proposed by artificial intelligence are decision aids: they may be incomplete or inaccurate and must be checked. No action is run without a user's approval and the permission of the relevant agent's configuration.

## 12. Customer's obligations

The customer:

- only installs agents and connects connectors on systems and accounts it is entitled to monitor;
- chooses the data it sends, and holds the rights needed to process it; it uses the filtering and masking tools to keep out what it does not wish to entrust to the Service;
- does not use the Service to probe or attack third-party systems, or for any unlawful use;
- makes reasonable use of features announced as unlimited; a clearly abnormal use (for example automated for purposes unrelated to monitoring) may be limited after informing the customer;
- keeps its billing information accurate.

S33D may suspend a workspace whose use endangers the Service or third parties, after informing the owner except in an emergency.

## 13. Customer data

The customer remains the owner of its data. S33D only uses it to provide the Service and neither sells it nor uses it for other purposes. For the personal data contained in monitoring data, S33D acts as **processor** for the customer, within the meaning of article 28 of the GDPR:

- it only processes this data on the customer's instructions, expressed by its configuration and use of the Service;
- the people who have access to it are bound by confidentiality;
- it implements the security measures described in the [documentation](/en/docs/security/);
- it only uses another processor under the conditions of the [privacy policy](/en/privacy/), which lists them;
- it helps the customer answer data subjects' requests and meet its security obligations;
- it notifies the customer of any personal data breach concerning it without undue delay after becoming aware of it;
- at the end of the Service, data is erased when the retention periods expire; the customer may request earlier erasure;
- it makes available the information needed to demonstrate compliance with these obligations.

The Entreprise plan includes a signed data processing agreement (DPA) detailing these commitments. S33D's processing of personal data as controller is described in the [privacy policy](/en/privacy/).

## 14. Liability

S33D is not liable for indirect damages (loss of revenue, customers, or data not backed up elsewhere), nor for the consequences of an alert not issued or of an action approved by the customer. Towards a business customer, S33D's total liability under the Service is limited to the amounts paid by the customer for the Service during the twelve months preceding the triggering event. These limits do not apply in case of gross or wilful misconduct, to bodily injury, or towards a consumer beyond what the law allows.

## 15. Intellectual property

The HYPHA software is distributed under the AGPL v3 (server and interface) and Apache 2.0 (agent) licences. The HYPHA and S33D trademarks, the texts and visuals of the site remain S33D's property.

## 16. Complaints and mediation

Any complaint should first be sent to [contact@s33d.fr](mailto:contact@s33d.fr). In accordance with article L.612-1 of the French Consumer Code, a consumer customer may then refer the matter free of charge to the consumer mediator designated by S33D, whose details appear in the terms of sale of [www.s33d.fr](https://www.s33d.fr/cgv), within one year of their written complaint.

## 17. Changes to the Terms and prices

S33D may change the Terms and prices. Any unfavourable change is announced by email to the workspace owner at least thirty days before it takes effect; the customer may cancel before that date at no cost other than the usage already incurred. Ongoing yearly commitments keep their price until they end.

## 18. Force majeure

Neither party is liable for a failure caused by a force majeure event within the meaning of article 1218 of the French Civil Code.

## 19. Governing law and jurisdiction

The Terms are governed by French law. Failing an amicable agreement, and where applicable after referral to the mediator, any dispute with a business customer falls under the exclusive jurisdiction of the **Douai Commercial Court** (Tribunal de commerce de Douai). A consumer customer may refer the matter to the court of their place of residence or the court provided for by law.
