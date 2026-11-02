-- Corrections to usage events (ADR 0027).

-- name: LockOriginal :exec
-- Serialises the corrections of one original event: held until the transaction ends, so a second correction reads the first one's difference.
SELECT pg_advisory_xact_lock(hashtextextended(@tenant_id::text || '/' || @event_id::text, 0));

-- name: CorrectedUnits :many
-- What corrections have moved each of the given events by, so far.
SELECT corrects_event_id, sum(delta_units)::bigint AS delta_units
FROM event_corrections
WHERE tenant_id = @tenant_id::uuid AND corrects_event_id = ANY(@event_ids::text[])
GROUP BY corrects_event_id;

-- name: WhichAreCorrections :many
-- Of the given event ids, the ones that are themselves corrections (a correction cannot be corrected).
SELECT event_id FROM event_corrections WHERE tenant_id = @tenant_id::uuid AND event_id = ANY(@event_ids::text[]);

-- name: InsertCorrections :exec
INSERT INTO event_corrections (tenant_id, event_id, correction_key, corrects_key, corrects_event_id, customer_id, meter_code, delta_units, reason, corrected_by)
SELECT @tenant_id::uuid, e.v, k.v, o.v, i.v, c.v, m.v, d.v, r.v, b.v
FROM unnest(@event_ids::text[]) WITH ORDINALITY AS e(v, n)
JOIN unnest(@correction_keys::text[]) WITH ORDINALITY AS k(v, n) USING (n)
JOIN unnest(@corrects_keys::text[]) WITH ORDINALITY AS o(v, n) USING (n)
JOIN unnest(@corrects_event_ids::text[]) WITH ORDINALITY AS i(v, n) USING (n)
JOIN unnest(@customer_ids::uuid[]) WITH ORDINALITY AS c(v, n) USING (n)
JOIN unnest(@meter_codes::text[]) WITH ORDINALITY AS m(v, n) USING (n)
JOIN unnest(@deltas::bigint[]) WITH ORDINALITY AS d(v, n) USING (n)
JOIN unnest(@reasons::text[]) WITH ORDINALITY AS r(v, n) USING (n)
JOIN unnest(@corrected_by::text[]) WITH ORDINALITY AS b(v, n) USING (n);
