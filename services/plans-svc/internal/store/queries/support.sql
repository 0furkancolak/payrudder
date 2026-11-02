-- The help desk (SupportService). Run as plans_system from internal/support only.
-- Both ticket selects return the same columns, so one function maps either row.

-- name: GetSupportTicket :one
SELECT t.id, t.number, t.account_id, a.name AS account_name, t.app_id, COALESCE(p.name, '')::text AS app_name, t.opened_by,
       COALESCE(NULLIF(u.display_name, ''), u.email)::text AS opened_by_name, t.subject, t.category, t.priority, t.status, t.assignee, t.last_author,
       t.first_response_due, t.first_responded_at, t.last_message_at, t.solved_at, t.created_at, t.updated_at,
       (CASE WHEN t.first_responded_at IS NULL THEN t.status = 'open' AND t.first_response_due < now() ELSE t.first_responded_at > t.first_response_due END)::boolean AS sla_breached,
       (SELECT count(*) FROM support_messages m WHERE m.ticket_id = t.id AND NOT m.internal)::int AS message_count,
       (COALESCE(t.opened_by = sqlc.narg('viewer')::uuid, false) AND t.opener_seen_at IS NULL)::boolean AS unread, t.tags,
       t.sla_tier, t.merged_into, mt.number AS merged_into_number, COALESCE(t.rating, 0)::int AS rating, t.rating_comment, t.rated_at
FROM support_tickets t
JOIN accounts a ON a.id = t.account_id
LEFT JOIN apps p ON p.id = t.app_id
LEFT JOIN support_tickets mt ON mt.id = t.merged_into
JOIN users u ON u.id = t.opened_by
WHERE t.id = @id;

-- A person's tickets are those of the accounts they are a (non-affiliate) member of; the operator passes no user and sees all.
-- name: ListSupportTickets :many
SELECT t.id, t.number, t.account_id, a.name AS account_name, t.app_id, COALESCE(p.name, '')::text AS app_name, t.opened_by,
       COALESCE(NULLIF(u.display_name, ''), u.email)::text AS opened_by_name, t.subject, t.category, t.priority, t.status, t.assignee, t.last_author,
       t.first_response_due, t.first_responded_at, t.last_message_at, t.solved_at, t.created_at, t.updated_at,
       (CASE WHEN t.first_responded_at IS NULL THEN t.status = 'open' AND t.first_response_due < now() ELSE t.first_responded_at > t.first_response_due END)::boolean AS sla_breached,
       (SELECT count(*) FROM support_messages m WHERE m.ticket_id = t.id AND NOT m.internal)::int AS message_count,
       (COALESCE(t.opened_by = sqlc.narg('user_id')::uuid, false) AND t.opener_seen_at IS NULL)::boolean AS unread, t.tags,
       t.sla_tier, t.merged_into, mt.number AS merged_into_number, COALESCE(t.rating, 0)::int AS rating, t.rating_comment, t.rated_at
FROM support_tickets t
JOIN accounts a ON a.id = t.account_id
LEFT JOIN apps p ON p.id = t.app_id
LEFT JOIN support_tickets mt ON mt.id = t.merged_into
JOIN users u ON u.id = t.opened_by
WHERE (sqlc.narg('user_id')::uuid IS NULL OR t.account_id IN (SELECT m.account_id FROM account_members m WHERE m.user_id = sqlc.narg('user_id')::uuid AND m.role <> 'affiliate'))
  AND (@status::text = '' OR t.status = @status::text OR (@status::text = 'active' AND t.status IN ('open', 'pending')))
  AND (@priority::text = '' OR t.priority = @priority::text)
  AND (@assignee::text = '' OR t.assignee = @assignee::text)
  AND (@tag::text = '' OR @tag::text = ANY(t.tags))
  AND (@search::text = '' OR t.subject ILIKE '%' || @search::text || '%' OR t.number::text = ltrim(@search::text, '#')
       -- The words of the messages (migration 00030): the person's list reads the public ones only, the operator's the notes too. @query is the text as typed.
       OR EXISTS (SELECT 1 FROM support_messages sm WHERE sm.ticket_id = t.id AND (@with_internal::boolean OR NOT sm.internal) AND sm.tsv @@ websearch_to_tsquery('simple', @query::text)))
  AND (NOT @sla_breached::boolean OR (t.first_responded_at IS NULL AND t.status = 'open' AND t.first_response_due < now()) OR t.first_responded_at > t.first_response_due)
ORDER BY t.last_message_at DESC, t.id
LIMIT 200;

-- Locks the ticket for a change.
-- name: LockSupportTicket :one
SELECT id, account_id, app_id, opened_by, status, priority, assignee, first_response_due, first_responded_at, solved_at, created_at, sla_zone, sla_tier, sla_app_id, tags, last_author, merged_into
FROM support_tickets WHERE id = @id FOR UPDATE;

-- The user's role in the account, if any; an affiliate of a program is not a member for the help desk.
-- name: SupportRoleOf :one
SELECT role FROM account_members WHERE account_id = @account_id AND user_id = @user_id AND role <> 'affiliate';

-- True when the app belongs to the account and is not archived.
-- name: SupportAppInAccount :one
SELECT EXISTS (SELECT 1 FROM apps WHERE id = @id AND account_id = @account_id AND archived_at IS NULL) AS found;

-- name: InsertSupportTicket :exec
INSERT INTO support_tickets (id, account_id, app_id, opened_by, subject, category, priority, status, last_author, first_response_due, sla_zone, sla_tier, sla_app_id)
VALUES (@id, @account_id, sqlc.narg('app_id')::uuid, @opened_by, @subject, @category, @priority, 'open', 'customer', @first_response_due, @sla_zone, @sla_tier, sqlc.narg('sla_app_id')::uuid);

-- name: InsertSupportMessage :exec
INSERT INTO support_messages (id, ticket_id, author_kind, author_user_id, author_name, internal, body)
VALUES (@id, @ticket_id, @author_kind, sqlc.narg('author_user_id')::uuid, @author_name, @internal, @body);

-- Every mutable column at once: the module reads the locked row, decides the new state in Go and writes it back.
-- name: SaveSupportTicket :exec
UPDATE support_tickets
SET status = @status, priority = @priority, assignee = @assignee, tags = @tags::text[], last_author = @last_author, first_response_due = @first_response_due,
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

-- One open-ticket count at a time per account: a second ticket opened at the same moment waits here, so the limit holds.
-- name: SupportLockAccount :exec
SELECT pg_advisory_xact_lock(hashtextextended('support:' || @account_id::text, 0));

-- Tickets that still need an answer or a decision (open or pending), for the limit per account.
-- name: SupportOpenCount :one
SELECT count(*)::int FROM support_tickets WHERE account_id = @account_id AND status IN ('open', 'pending');

-- What the first-answer deadline is counted by: the time zone and the app whose days off apply (the ticket's app, else the account's oldest app, else UTC and the nil
-- uuid for none),
-- and the plan of the ticket's app (its live tenant's; the best among the account's apps when the ticket names none; free when it has no app yet).
-- name: SupportClockFor :one
SELECT COALESCE(z.zone, 'UTC')::text AS zone, COALESCE(z.app_id, '00000000-0000-0000-0000-000000000000'::uuid) AS clock_app_id,
       COALESCE((SELECT t.tier FROM tenants t JOIN apps p ON p.id = t.app_id
                 WHERE t.account_id = @account_id AND p.archived_at IS NULL AND (sqlc.narg('app_id')::uuid IS NULL OR p.id = sqlc.narg('app_id')::uuid)
                 ORDER BY (t.environment = 'live') DESC, CASE t.tier WHEN 'enterprise' THEN 0 WHEN 'standard' THEN 1 ELSE 2 END LIMIT 1), 'free')::text AS tier
FROM (SELECT 1) one
LEFT JOIN LATERAL (
  SELECT t.timezone AS zone, p.id AS app_id FROM tenants t JOIN apps p ON p.id = t.app_id
  WHERE t.account_id = @account_id AND p.archived_at IS NULL
  ORDER BY COALESCE(p.id = sqlc.narg('app_id')::uuid, false) DESC, p.created_at, t.environment DESC LIMIT 1
) z ON true;

-- The days off of an app, as calendar days.
-- name: ListSupportHolidays :many
SELECT to_char(day, 'YYYY-MM-DD')::text AS day, name FROM support_holidays WHERE app_id = @app_id ORDER BY day;

-- name: DeleteSupportHolidays :exec
DELETE FROM support_holidays WHERE app_id = @app_id;

-- name: InsertSupportHoliday :exec
INSERT INTO support_holidays (app_id, day, name) VALUES (@app_id, (@day::text)::date, @name);

-- The app, as the holiday calls need it: its name, and that it exists and is not archived.
-- name: SupportAppName :one
SELECT name FROM apps WHERE id = @id AND archived_at IS NULL;

-- name: InsertSupportEvent :exec
INSERT INTO support_events (id, ticket_id, kind, actor_kind, actor_name, from_value, to_value)
VALUES (@id, @ticket_id, @kind, @actor_kind, @actor_name, @from_value, @to_value);

-- name: ListSupportEvents :many
SELECT id, kind, actor_kind, actor_name, from_value, to_value, created_at FROM support_events WHERE ticket_id = @ticket_id ORDER BY created_at, id;

-- The opener has read the ticket (or wrote the last word on it): the "new" mark goes. Only the opener's own call clears it.
-- name: MarkSupportTicketSeen :exec
UPDATE support_tickets SET opener_seen_at = now() WHERE id = @id AND opened_by = @user_id AND opener_seen_at IS NULL;

-- An answer or a status change by the operator that the opener has not read.
-- name: MarkSupportTicketUnseen :exec
UPDATE support_tickets SET opener_seen_at = NULL WHERE id = @id;

-- What a mail about a ticket names, and who gets it: the person who opened it.
-- name: SupportMailTarget :one
SELECT t.number, t.subject, t.priority, t.status, a.name AS account_name, u.email AS opener_email, u.locale AS opener_locale
FROM support_tickets t JOIN accounts a ON a.id = t.account_id JOIN users u ON u.id = t.opened_by
WHERE t.id = @id;

-- The language an operator's address reads mail in, for the operators who have a user (the others read English).
-- name: SupportOperatorLocales :many
SELECT lower(email)::text AS email, locale FROM users WHERE lower(email) = ANY(@emails::text[]);

-- Closes the tickets that wait for the person and have waited since before the given time (the last time they became "pending", else their last message), at most
-- a batch, and says which. Rows another instance is already closing are skipped.
-- name: SupportCloseStale :many
UPDATE support_tickets
SET status = 'closed', solved_at = COALESCE(solved_at, now()), opener_seen_at = NULL, updated_at = now()
WHERE id IN (
  SELECT s.id FROM support_tickets s
  WHERE s.status = 'pending'
    AND COALESCE((SELECT max(e.created_at) FROM support_events e WHERE e.ticket_id = s.id AND e.kind = 'status' AND e.to_value = 'pending'), s.last_message_at) < @before::timestamptz
  ORDER BY s.last_message_at LIMIT @batch::int FOR UPDATE SKIP LOCKED
)
RETURNING id;

-- The addresses an operator is known by, with the name of the user who has one (the others have none).
-- name: SupportOperatorNames :many
SELECT lower(email)::text AS email, COALESCE(NULLIF(display_name, ''), '')::text AS name FROM users WHERE lower(email) = ANY(@emails::text[]);

-- Tickets the person opened that have news they have not read: the badge on the help page's link.
-- name: SupportUnreadCount :one
SELECT count(*)::int FROM support_tickets
WHERE opened_by = @user_id AND opener_seen_at IS NULL
  AND account_id IN (SELECT m.account_id FROM account_members m WHERE m.user_id = @user_id AND m.role <> 'affiliate');

-- The queue: what waits for the operator (open), and how much of it has no first answer past its deadline.
-- name: SupportQueueCounts :one
SELECT (count(*) FILTER (WHERE status = 'open'))::int AS waiting,
       (count(*) FILTER (WHERE status = 'open' AND first_responded_at IS NULL AND first_response_due < now()))::int AS breached
FROM support_tickets;

-- name: SupportCountsByStatus :many
SELECT status::text AS value, count(*)::int AS count FROM support_tickets GROUP BY status ORDER BY status;

-- name: SupportActiveCountsByPriority :many
SELECT priority::text AS value, count(*)::int AS count FROM support_tickets WHERE status IN ('open', 'pending') GROUP BY priority ORDER BY priority;

-- The first answers of the tickets opened in the last @days days: wall-clock seconds from opening to the operator's first public answer.
-- name: SupportFirstAnswerStats :one
SELECT count(*)::int AS opened, count(first_responded_at)::int AS answered,
       (count(*) FILTER (WHERE first_responded_at IS NOT NULL AND first_responded_at <= first_response_due))::int AS answered_on_time,
       COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM first_responded_at - created_at)), 0)::bigint AS median_seconds,
       COALESCE(avg(extract(epoch FROM first_responded_at - created_at)), 0)::bigint AS average_seconds
FROM support_tickets WHERE created_at >= now() - make_interval(days => @days::int);

-- The tickets that wait for an answer past their deadline, by priority: the gauge an alert watches.
-- name: SupportBreachedByPriority :many
SELECT priority::text AS priority, count(*)::int AS count FROM support_tickets
WHERE status = 'open' AND first_responded_at IS NULL AND first_response_due < now() GROUP BY priority;

-- name: ListSupportCannedReplies :many
SELECT id, title, body, updated_by, updated_at FROM support_canned_replies ORDER BY lower(title), id;

-- name: InsertSupportCannedReply :one
INSERT INTO support_canned_replies (id, title, body, updated_by) VALUES (@id, @title, @body, @updated_by) RETURNING id, title, body, updated_by, updated_at;

-- name: UpdateSupportCannedReply :one
UPDATE support_canned_replies SET title = @title, body = @body, updated_by = @updated_by, updated_at = now() WHERE id = @id RETURNING id, title, body, updated_by, updated_at;

-- name: DeleteSupportCannedReply :execrows
DELETE FROM support_canned_replies WHERE id = @id;

-- --- attachments (plan 12). The bytes are in assets-svc; these rows are the record.

-- The message as the attachment calls need it: whose it is, whether it is a note, and the ticket and account it belongs to.
-- name: SupportMessageForAttachment :one
SELECT m.id, m.ticket_id, m.author_kind, m.author_user_id, m.internal, t.account_id, t.status
FROM support_messages m JOIN support_tickets t ON t.id = m.ticket_id
WHERE m.id = @id AND m.ticket_id = @ticket_id;

-- The tenant whose storage holds an account's help desk files: the oldest live app's live tenant, else the oldest app's sandbox tenant. No row: the account has
-- no app yet.
-- name: SupportStorageTenant :one
SELECT t.id, t.environment, t.timezone, t.tier
FROM tenants t JOIN apps p ON p.id = t.app_id
WHERE t.account_id = @account_id AND p.archived_at IS NULL
ORDER BY (t.environment = 'live') DESC, p.created_at, t.id LIMIT 1;

-- True when the tenant belongs to the account (the gateway names the tenant it stored under; it must be one of the account's).
-- name: SupportTenantInAccount :one
SELECT t.id, t.environment, t.timezone, t.tier FROM tenants t WHERE t.id = @id AND t.account_id = @account_id;

-- name: SupportAttachmentTotals :one
SELECT count(*) FILTER (WHERE a.ticket_id = @ticket_id)::int AS on_ticket, COALESCE(sum(a.size_bytes), 0)::bigint AS account_bytes
FROM support_attachments a JOIN support_tickets t ON t.id = a.ticket_id
WHERE t.account_id = @account_id;

-- name: InsertSupportAttachment :exec
INSERT INTO support_attachments (id, ticket_id, message_id, storage_tenant_id, asset_id, filename, content_type, size_bytes)
VALUES (@id, @ticket_id, @message_id, @storage_tenant_id, @asset_id, @filename, @content_type, @size_bytes);

-- The attachments of a ticket's messages (those of internal notes only when asked for), oldest first.
-- name: ListSupportAttachments :many
SELECT a.id, a.message_id, a.filename, a.content_type, a.size_bytes, a.created_at
FROM support_attachments a JOIN support_messages m ON m.id = a.message_id
WHERE a.ticket_id = @ticket_id AND (@with_internal::boolean OR NOT m.internal)
ORDER BY a.created_at, a.id;

-- One attachment with where it is stored and whether its message is an internal note.
-- name: GetSupportAttachment :one
SELECT a.id, a.ticket_id, a.storage_tenant_id, a.asset_id, a.filename, m.internal, t.account_id, tn.environment, tn.timezone, tn.tier
FROM support_attachments a
JOIN support_messages m ON m.id = a.message_id
JOIN support_tickets t ON t.id = a.ticket_id
JOIN tenants tn ON tn.id = a.storage_tenant_id
WHERE a.id = @id AND a.ticket_id = @ticket_id;

-- --- ratings, merges and links (plan 12, migration 00030)

-- name: RateSupportTicket :exec
UPDATE support_tickets SET rating = @rating::smallint, rating_comment = @comment, rated_at = now(), updated_at = now() WHERE id = @id;

-- The tickets rated in the last @days days: how many and the average, and how many gave each rating.
-- name: SupportRatingStats :one
SELECT count(*)::int AS rated, COALESCE(avg(rating), 0)::float8 AS average FROM support_tickets WHERE rated_at >= now() - make_interval(days => @days::int);

-- name: SupportRatingCounts :many
SELECT rating::int AS value, count(*)::int AS count FROM support_tickets WHERE rated_at >= now() - make_interval(days => @days::int) GROUP BY rating ORDER BY rating;

-- A merge moves the messages and the files of one ticket to another; the ticket they left is closed and points at the other.
-- name: MoveSupportMessages :exec
UPDATE support_messages SET ticket_id = @into_id WHERE ticket_id = @from_id;

-- name: MoveSupportAttachments :exec
UPDATE support_attachments SET ticket_id = @into_id WHERE ticket_id = @from_id;

-- name: SetSupportTicketMerged :exec
UPDATE support_tickets SET merged_into = @into_id, updated_at = now() WHERE id = @id;

-- name: SupportTicketNumber :one
SELECT COALESCE(number, 0)::bigint FROM support_tickets WHERE id = @id;

-- The tenant of an app of the account in an environment (a link's target); no row when the app is not the account's, is archived, or has no such environment.
-- name: SupportLinkTenant :one
SELECT t.id FROM tenants t JOIN apps p ON p.id = t.app_id
WHERE t.account_id = @account_id AND p.id = @app_id AND p.archived_at IS NULL AND t.environment = @environment;

-- name: DeleteSupportLinks :exec
DELETE FROM support_ticket_links WHERE ticket_id = @ticket_id;

-- name: InsertSupportLink :exec
INSERT INTO support_ticket_links (id, ticket_id, link_tenant_id, kind, ref) VALUES (@id, @ticket_id, @link_tenant_id, @kind, @ref);

-- name: ListSupportLinks :many
SELECT l.kind, l.ref, t.app_id, t.environment, l.link_tenant_id, COALESCE(p.name, '')::text AS app_name
FROM support_ticket_links l JOIN tenants t ON t.id = l.link_tenant_id LEFT JOIN apps p ON p.id = t.app_id
WHERE l.ticket_id = @ticket_id ORDER BY l.created_at, l.id;
