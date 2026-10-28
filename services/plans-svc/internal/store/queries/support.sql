-- The help desk (SupportService). Run as plans_system from internal/support only.
-- Both ticket selects return the same columns, so one function maps either row.

-- name: GetSupportTicket :one
SELECT t.id, t.number, t.account_id, a.name AS account_name, t.app_id, COALESCE(p.name, '')::text AS app_name, t.opened_by,
       COALESCE(NULLIF(u.display_name, ''), u.email)::text AS opened_by_name, t.subject, t.category, t.priority, t.status, t.assignee, t.last_author,
       t.first_response_due, t.first_responded_at, t.last_message_at, t.solved_at, t.created_at, t.updated_at,
       (CASE WHEN t.first_responded_at IS NULL THEN t.status = 'open' AND t.first_response_due < now() ELSE t.first_responded_at > t.first_response_due END)::boolean AS sla_breached,
       (SELECT count(*) FROM support_messages m WHERE m.ticket_id = t.id AND NOT m.internal)::int AS message_count
FROM support_tickets t
JOIN accounts a ON a.id = t.account_id
LEFT JOIN apps p ON p.id = t.app_id
JOIN users u ON u.id = t.opened_by
WHERE t.id = @id;

-- A person's tickets are those of the accounts they are a (non-affiliate) member of; the operator passes no user and sees all.
-- name: ListSupportTickets :many
SELECT t.id, t.number, t.account_id, a.name AS account_name, t.app_id, COALESCE(p.name, '')::text AS app_name, t.opened_by,
       COALESCE(NULLIF(u.display_name, ''), u.email)::text AS opened_by_name, t.subject, t.category, t.priority, t.status, t.assignee, t.last_author,
       t.first_response_due, t.first_responded_at, t.last_message_at, t.solved_at, t.created_at, t.updated_at,
       (CASE WHEN t.first_responded_at IS NULL THEN t.status = 'open' AND t.first_response_due < now() ELSE t.first_responded_at > t.first_response_due END)::boolean AS sla_breached,
       (SELECT count(*) FROM support_messages m WHERE m.ticket_id = t.id AND NOT m.internal)::int AS message_count
FROM support_tickets t
JOIN accounts a ON a.id = t.account_id
LEFT JOIN apps p ON p.id = t.app_id
JOIN users u ON u.id = t.opened_by
WHERE (sqlc.narg('user_id')::uuid IS NULL OR t.account_id IN (SELECT m.account_id FROM account_members m WHERE m.user_id = sqlc.narg('user_id')::uuid AND m.role <> 'affiliate'))
  AND (@status::text = '' OR t.status = @status::text OR (@status::text = 'active' AND t.status IN ('open', 'pending')))
  AND (@priority::text = '' OR t.priority = @priority::text)
  AND (@assignee::text = '' OR t.assignee = @assignee::text)
  AND (@search::text = '' OR t.subject ILIKE '%' || @search::text || '%' OR t.number::text = ltrim(@search::text, '#'))
  AND (NOT @sla_breached::boolean OR (t.first_responded_at IS NULL AND t.status = 'open' AND t.first_response_due < now()) OR t.first_responded_at > t.first_response_due)
ORDER BY t.last_message_at DESC, t.id
LIMIT 200;

-- Locks the ticket for a change.
-- name: LockSupportTicket :one
SELECT id, account_id, status, priority, assignee, first_response_due, first_responded_at, solved_at, created_at
FROM support_tickets WHERE id = @id FOR UPDATE;

-- The user's role in the account, if any; an affiliate of a program is not a member for the help desk.
-- name: SupportRoleOf :one
SELECT role FROM account_members WHERE account_id = @account_id AND user_id = @user_id AND role <> 'affiliate';

-- True when the app belongs to the account and is not archived.
-- name: SupportAppInAccount :one
SELECT EXISTS (SELECT 1 FROM apps WHERE id = @id AND account_id = @account_id AND archived_at IS NULL) AS found;

-- name: InsertSupportTicket :exec
INSERT INTO support_tickets (id, account_id, app_id, opened_by, subject, category, priority, status, last_author, first_response_due)
VALUES (@id, @account_id, sqlc.narg('app_id')::uuid, @opened_by, @subject, @category, @priority, 'open', 'customer', @first_response_due);

-- name: InsertSupportMessage :exec
INSERT INTO support_messages (id, ticket_id, author_kind, author_user_id, author_name, internal, body)
VALUES (@id, @ticket_id, @author_kind, sqlc.narg('author_user_id')::uuid, @author_name, @internal, @body);

-- Every mutable column at once: the module reads the locked row, decides the new state in Go and writes it back.
-- name: SaveSupportTicket :exec
UPDATE support_tickets
SET status = @status, priority = @priority, assignee = @assignee, last_author = @last_author, first_response_due = @first_response_due,
    first_responded_at = sqlc.narg('first_responded_at')::timestamptz, solved_at = sqlc.narg('solved_at')::timestamptz,
    last_message_at = CASE WHEN @touch::boolean THEN now() ELSE last_message_at END, updated_at = now()
WHERE id = @id;

-- name: ListSupportMessages :many
SELECT id, author_kind, author_name, internal, body, created_at
FROM support_messages WHERE ticket_id = @ticket_id AND (@with_internal::boolean OR NOT internal)
ORDER BY created_at, id;

-- How a person is shown in a thread.
-- name: SupportAuthorName :one
SELECT COALESCE(NULLIF(display_name, ''), email)::text AS name FROM users WHERE id = @id;
