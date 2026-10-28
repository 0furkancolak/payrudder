# 12 — Support system, finished

## The request
List 1 (1): an **advanced support system**.

## Where we are
#244: tickets between the people who run apps (every member of an account sees the account's tickets) and the operator; categories, priorities with first-answer deadlines (urgent 1 h … low 72 h), statuses (open = waiting for us, pending = waiting for the customer, solved, closed), assignee (free text), threads with the operator's **internal notes**, an operator inbox with filters, "late" detection, sort, bulk status/priority/assignee, a "Help" page for the organisation; `SupportService` in plans-svc, gateway routes, console. Tested by an integration test and mock e2e.

## Gaps
- **No notifications.** Nobody is mailed when a ticket is opened or answered (neither the operator nor the person). The inbox polls every 30 s. This makes it a message board, not a support system.
- No attachments (screenshots, logs), no rich text, no links to the object concerned (an invoice, a customer, a payment) even though peek/links exist.
- SLA is wall-clock hours, no business hours or time zones, no breach escalation, no first-response/resolution reports.
- Assignee is free text (no operator user list), no tags, no canned replies, no merge/duplicate, no satisfaction rating, no search over message bodies, no ticket history/audit (status changes are not logged), no auto-close of solved tickets after N days, no rate limit or spam protection on opening tickets, no limits on attachments or body beyond 10 000 characters.
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
