# 12 — Support system, finished

## The request
List 1 (1): an **advanced support system**.

## Where we are
**Status 2026-11-02 (PR "Support: mail, history, business hours, limits, auto-close"): scope (a) is finished as a mailing help desk; the rest below is open.** Added on top of #244: ticket mail through plans-svc `usermail` in six languages (a new ticket to the operators, the operator's answer and a status the operator sets to the person who opened it), an append-only history of status, priority and assignee changes shown in the console thread (`support_events`), first-answer deadlines in business hours (Monday to Friday 09:00 to 18:00 in the time zone of the ticket's app), an "unread" mark for the person who opened the ticket, at most 20 tickets waiting per account (`support.tickets.too_many`), and a job that closes a ticket that waited for the person for 7 days. Details: [plans-svc.md, "the help desk"](../03-services/plans-svc.md). **Defaults (not agreed)**, all of them the owner's to overturn: scope (a) only; business hours Monday to Friday 09:00 to 18:00 in the app's zone, counted for every priority (urgent 1 h, high 4 h, normal 9 h = one business day, low 27 h = three business days; the old 24 h and 72 h were wall-clock); mail is transactional with no opt-out, links only (no ticket text in the mail); auto-close after 7 days waiting for the person (`PAYRUDDER_SUPPORT_AUTOCLOSE_AFTER`); 20 waiting tickets per account; the operators mailed are `PAYRUDDER_OPERATOR_EMAILS`, now also read by plans-svc; attachments skipped (built in the next status).

**Before this status:** #244: tickets between the people who run apps (every member of an account sees the account's tickets) and the operator; categories, priorities with first-answer deadlines (urgent 1 h … low 72 h), statuses (open = waiting for us, pending = waiting for the customer, solved, closed), assignee (free text), threads with the operator's **internal notes**, an operator inbox with filters, "late" detection, sort, bulk status/priority/assignee, a "Help" page for the organisation; `SupportService` in plans-svc, gateway routes, console. Tested by an integration test and mock e2e.

**Status 2026-11-02 (second PR, branch `feat/product-tail-3`): attachments, sidebar counts, a breach alert, operator assignees and a mail on the person's reply.** Built: (1) **attachments**: png, jpeg, pdf and plain text, 5 MiB each, 10 per ticket, 50 MiB per organisation, stored by assets-svc (new purpose `support_attachment`, migration assets-svc `00002`) under the first tenant of the ticket's account, recorded in `support_attachments` (plans-svc `00021`), added by the person to their own messages and by the operator to the operator's (notes included), downloaded through a presigned link; the person never sees the files of an internal note. (2) **Counts in the sidebar**: the person's tickets with news they have not read, the operators' tickets waiting for an answer (red when some are late). (3) **SLA-breach gauges** `support_tickets_waiting` and `support_tickets_sla_breached` and the alert `SupportAnswerOverdue` (15 minutes, warn). (4) **Assignee** is one of `PAYRUDDER_OPERATOR_EMAILS` (a picker in the inbox and in the bulk bar; free text where none is configured). (5) **The person's answer is mailed to the operators** (`ticket_customer_reply`, six languages), to the assignee alone when there is one, and only when the ticket was not already waiting for support. Canned replies do not exist and were not started. The inbox is now an operator path in the console (`/support` was missing from `OPERATOR_PATHS`, so the operator's sidebar was not shown there). **Defaults (not agreed)**: the files go under the live tenant of the account's oldest app and an account without an app cannot attach (`support.attachment.no_app`); the limits above; they count against that tenant's storage quota; no malware scan, no retention; an assignee must be an operator address when any is configured; the operators' badge is "waiting for an answer" (there is no per-operator read mark); the alert fires after 15 minutes. **Not verified**: an upload through a real assets-svc and S3 from the gateway end to end (assets-svc's integration test and the gateway's fakes cover the two halves), the screens at phone width, real mail.

## Gaps
Closed by the 2026-11-02 status above: notifications to the operators and to the person (the inbox still polls every 30 s), business hours and time zones in the SLA, the history of changes, auto-close, a limit of waiting tickets per account. Open:
- ~~No attachments~~ (built, see the status above; no scan, no retention). No rich text, no links to the object concerned (an invoice, a customer, a payment) even though peek/links exist.
- SLA: the breach alert is built; no holiday calendar, no first-response/resolution reports, no SLA by plan.
- Assignee is an operator address where operators are configured; no tags, no canned replies, no merge/duplicate, no satisfaction rating, no search over message bodies. Opening a ticket is rate-limited per user by the gateway (class write) and capped at 20 waiting per account; there is no other spam protection and no limit on attachments (none exist) or body beyond 10 000 characters.
- Customers of the seller (the end customers) are not served: this is the help desk **between Payrudder users and the operator** only. The owner's sentence may mean also a help desk for the seller's own customers.
- No real-time updates, no web push; no email-to-ticket (reply by e-mail).
- Operators are identified by a shared operator context; the message "author name" is the operator's e-mail from the session.

## Research first
1. Ask the owner which help desk is meant: (a) Payrudder ↔ its users (built, thin), (b) seller ↔ the seller's customers (a product feature the sellers would use), (c) both.
2. Study Intercom, Zendesk, Help Scout, Plain: the minimum a team needs (queues, assignment, macros, SLAs, reports, email threading) and choose the core.
3. Mail threading and inbound mail needs Nafru (11): confirm inbound capability.

## Decisions needed
Scope (a/b/c), SLA policy per plan (SaaS tier priority), channels (console only, e-mail, chat), retention, who the operators are (roles).

## Work
1. Spec per chosen scope in this file.
2. Notifications first: mail to the operator team on new/urgent tickets; mail to the person on an operator answer; in-console unread badge.
3. Attachments (assets-svc exists), object links, history/audit log, assignee as operator users, canned replies, tags, auto-close.
4. SLA by plan, business hours, breach alerts (Alertmanager, 16), reports.
5. Email-to-ticket if inbound mail is available.
6. Abuse limits and tests (security: access across accounts, internal notes never leak, attachment scanning).

## Done means
A person can open, attach, be mailed on every answer and reply by mail; the operator sees SLA breaches in alerts; reports exist; security tests pass.

## Not verifiable here
Mail delivery (11), real operator workflow.

## Risks
Scope creep: pick (a/b/c) first.

## Review addendum (2026-11-01)
- **Corrections:** opening a ticket is rate-limited per user (session routes are charged in class write, `gateway/internal/httpapi/server.go`); operators are verified sessions listed in `PAYRUDDER_OPERATOR_EMAILS` or the machine token (`support.go`), not a shared context.
- **Mail does not wait for Nafru:** plans-svc `usermail` already mails Payrudder users in six languages over SMTP (`services/plans-svc/internal/identity/usermail`). Outbound ticket mail uses it now; Nafru is needed only for inbound mail.
- Support tables are account-level (global, no `tenant_id` or RLS); assets-svc is tenant-keyed, so attachments need a rule (default: store under the account's first tenant with a ticket purpose; marked default, not agreed).
- **Work to add:** public migration 00005 (it is `00007_support_history.sql` now), `support.*` error keys (`go test ./pkg/errkey`), OpenAPI `/app/v1/support` and `/admin/v1/support`, `make gen`, six locale files, tests for cross-account access and internal notes. *Done for the new parts (2026-11-02); the cross-account and internal-note tests were already in #244's integration test.*
- **Defaults (not agreed):** SLA business hours Mon-Fri 09-18 in the account's time zone; auto-close after 7 days awaiting the customer; at most 20 open tickets per account.


## Verification of the 2026-11-02 status
Run: unit tests of `domain` (business-hours deadline across a day end, a weekend, before opening, a Saturday ticket, an unknown zone), `usermail` (every language has the three new mails and the same placeholders, a ticket subject is escaped and not filled in twice), `go test ./pkg/errkey`; plans-svc integration `TestPlansSvc/Support` and `TestPlansSvc/SupportDesk` (mails and their language, unread for the opener only, the history in order and append-only, 20 waiting tickets also when opened at the same moment (fails without the lock), auto-close including a ticket set to pending again yesterday); the console's mock e2e `support.spec.ts` in local Chrome. **Not verified:** real mail delivery (Mailpit/Nafru/SMTP), the wording of the mails by a native speaker, the console in a real browser against the real API, the screens at phone width.
