# 12 — Support system, finished

**Status (2026-11-02, second round): the operator's side is now a desk.** Built on top of the first round below: the operators are mailed when the person answers them, an unread badge for the person (the help link) and a waiting badge for the operators (the inbox link), canned replies, tags on tickets, an assignee chosen from the operators, a report of the queue and of the first-answer times, and a gauge with an alert (`SupportFirstAnswerOverdue`) on tickets waiting past their deadline. **Attachments are not built** (the reasons are below). Still open: object links, SLA by plan, a holiday calendar, merge, satisfaction, search over bodies, email-to-ticket, real-time updates, help for the seller's own customers (scope b).

## The request
List 1 (1): an **advanced support system**.

## Where we are
**Status 2026-11-02 (PR "Support: mail, history, business hours, limits, auto-close"): scope (a) is finished as a mailing help desk; the rest below is open.** Added on top of #244: ticket mail through plans-svc `usermail` in six languages (a new ticket to the operators, the operator's answer and a status the operator sets to the person who opened it), an append-only history of status, priority and assignee changes shown in the console thread (`support_events`), first-answer deadlines in business hours (Monday to Friday 09:00 to 18:00 in the time zone of the ticket's app), an "unread" mark for the person who opened the ticket, at most 20 tickets waiting per account (`support.tickets.too_many`), and a job that closes a ticket that waited for the person for 7 days. Details: [plans-svc.md, "the help desk"](../03-services/plans-svc.md). **Defaults (not agreed)**, all of them the owner's to overturn: scope (a) only; business hours Monday to Friday 09:00 to 18:00 in the app's zone, counted for every priority (urgent 1 h, high 4 h, normal 9 h = one business day, low 27 h = three business days; the old 24 h and 72 h were wall-clock); mail is transactional with no opt-out, links only (no ticket text in the mail); auto-close after 7 days waiting for the person (`PAYRUDDER_SUPPORT_AUTOCLOSE_AFTER`); 20 waiting tickets per account; the operators mailed are `PAYRUDDER_OPERATOR_EMAILS`, now also read by plans-svc; attachments skipped.

**Before this status:** #244: tickets between the people who run apps (every member of an account sees the account's tickets) and the operator; categories, priorities with first-answer deadlines (urgent 1 h … low 72 h), statuses (open = waiting for us, pending = waiting for the customer, solved, closed), assignee (free text), threads with the operator's **internal notes**, an operator inbox with filters, "late" detection, sort, bulk status/priority/assignee, a "Help" page for the organisation; `SupportService` in plans-svc, gateway routes, console. Tested by an integration test and mock e2e.

## Gaps
Closed by the 2026-11-02 status above: notifications to the operators and to the person (the inbox still polls every 30 s; a customer's reply to the operator is not mailed yet), business hours and time zones in the SLA, the history of changes, auto-close, a limit of waiting tickets per account. Open:
- No attachments (screenshots, logs): assets-svc is keyed by tenant and a ticket belongs to an account, so a rule is needed first (the review's default: store under the account's first tenant with a ticket purpose). No rich text, no links to the object concerned (an invoice, a customer, a payment) even though peek/links exist.
- SLA has no breach escalation (an alert), no holiday calendar, no first-response/resolution reports, no SLA by plan.
- Assignee is free text (no operator user list), no tags, no canned replies, no merge/duplicate, no satisfaction rating, no search over message bodies, no count of unread tickets in the sidebar. Opening a ticket is rate-limited per user by the gateway (class write) and capped at 20 waiting per account; there is no other spam protection and no limit on attachments (none exist) or body beyond 10 000 characters.
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

## Leftovers built (2026-11-02, branch `feat/product-leftovers-2-p12`)

Defaults the implementer chose (**default, not agreed**):

1. **Mail to the operators when the person answers.** One mail to every operator address (`ticket_customer_reply`, six languages, in the language of the operator's user), but only when the ticket was last answered by an operator: a second message in a row adds none (the ticket is in the queue and in their sidebar badge already). Why not every message: a person writing four lines in a row would send four mails. Links only; no ticket text in the mail (as for the others).
2. **Badges.** The person's: tickets they opened with an answer or a status change they have not read (`CountMySupportUnread`). The operators': tickets waiting for them (status open), from the report. Both refresh every minute and after a change; an unreachable count shows nothing.
3. **Canned replies** are shared by all operators (no per-operator ones), plain text, no placeholders, no categories; a change is everybody's and shows who saved it last.
4. **Tags** are the operator's labels: at most 5 per ticket, lower case letters, digits and hyphens, at most 30 characters; never shown to the person; filterable; in the history. No tag list or colours.
5. **Assignee** is an operator address from `PAYRUDDER_OPERATOR_EMAILS` (the one list the gateway lets into the console and the mails go to). A ticket keeps an assignee that is not on the list (one typed by hand before) until it is changed. **Not built:** operator roles, "my tickets", automatic assignment.
6. **Report:** counts of the queue and the first answers of the **last 30 days** (wall clock, not business hours: the deadline is in business hours, the time people feel is not), median and average, answered in time. Not built: resolution time, per-operator figures, a chart, a period choice.
7. **SLA alert:** gauge `support_sla_breached_tickets{priority}` (and `support_waiting_tickets`) from plans-svc's own database; `SupportFirstAnswerOverdue` is a **warn** after 10 minutes with any ticket late (not a page: an urgent ticket that is late is a person's business, not a night call; raise it if the owner wants). A rule test (`promtool test rules`) holds it firing and resolving.
8. **Attachments: not built, and why.** assets-svc keys every file by tenant and by a purpose list in its proto (`invoice_logo`, invoice attachments, general files), and counts it against that tenant's storage quota. A ticket belongs to an **account**, so the review's default (store under the account's first tenant with a ticket purpose) would (a) spend the seller's own storage quota on evidence for support, (b) lose the evidence when that tenant is deleted or its sandbox reset, (c) need an operator path into another tenant's assets, which assets-svc does not have (every read is for the tenant of the credential), and (d) leave the plan's "attachment scanning" unanswered (assets-svc checks size and type, not content). A safe version needs an account-level purpose and quota in assets-svc, an operator read, and a scanner; that is its own plan, not a leftover. Until then a ticket takes text only, and the person pastes a link.

Tests: plans-svc integration `TestPlansSvc/SupportDeskExtras` (the operator mail once until the operator answers again, the unread count and its clearing, tags normalised / in the history / filtered / **never in what the person sees**, the assignee rules, the report as differences, the gauge read through the OpenTelemetry SDK with a 0 for a priority with none, canned replies and their validation, the operator-only calls refused), `Support` and `SupportDesk` (assignee now an operator address), unit tests of tags and assignee (`domain`), the six-language mail texts (`usermail`), gateway `TestSupportDeskRoutes` and the OpenAPI route check, `promtool test rules`, console unit tests (`duration`, the operator paths) and the mock e2e `support.spec.ts` (tags, canned replies, the report with the sidebar badge, the help link's badge). **Not verified here:** the mail in a real mailbox and its wording by a native speaker; the badges and the report against the real API in a browser; the gauge on a running Prometheus (the rule is checked with promtool); the screens at phone width and in Arabic (right to left).
