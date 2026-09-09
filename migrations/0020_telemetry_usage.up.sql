-- Atomic cutover: reads continue, but telemetry writes wait during backfill.
BEGIN;
SET LOCAL lock_timeout = '5s';
LOCK TABLE events, transactions, logs IN SHARE ROW EXCLUSIVE MODE;

-- Parent deletion is handled by the base-table cascade and usage triggers.
CREATE TABLE telemetry_usage (
    kind text NOT NULL,
    project_id uuid NOT NULL,
    bucket timestamptz NOT NULL,
    n bigint NOT NULL,
    PRIMARY KEY (kind, project_id, bucket)
);

CREATE FUNCTION adjust_telemetry_usage(k text, p uuid, b timestamptz, delta bigint)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE result bigint;
BEGIN
    IF delta = 0 THEN RETURN; END IF;
    INSERT INTO telemetry_usage(kind, project_id, bucket, n) VALUES(k, p, b, delta)
    ON CONFLICT(kind, project_id, bucket) DO UPDATE
    SET n = telemetry_usage.n + EXCLUDED.n RETURNING n INTO result;
    IF result < 0 THEN RAISE EXCEPTION 'negative telemetry usage'; END IF;
    IF result = 0 THEN
        DELETE FROM telemetry_usage WHERE kind=k AND project_id=p AND bucket=b;
    END IF;
END;
$$;

CREATE FUNCTION track_telemetry_usage() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE item record;
BEGIN
    IF TG_OP = 'TRUNCATE' THEN
        DELETE FROM telemetry_usage WHERE kind=TG_TABLE_NAME;
    ELSIF TG_OP = 'UPDATE' THEN
        FOR item IN
            SELECT project_id, bucket, sum(delta)::bigint AS delta FROM (
                SELECT project_id, date_trunc('hour', received_at, 'UTC'), -1 FROM old_rows
                UNION ALL SELECT project_id, date_trunc('hour', received_at, 'UTC'), 1 FROM new_rows
            ) AS changes(project_id, bucket, delta)
            GROUP BY project_id, bucket HAVING sum(delta) <> 0 ORDER BY project_id, bucket
        LOOP
            PERFORM adjust_telemetry_usage(TG_TABLE_NAME, item.project_id, item.bucket, item.delta);
        END LOOP;
    ELSIF TG_OP = 'INSERT' THEN
        FOR item IN SELECT project_id, date_trunc('hour', received_at, 'UTC') AS bucket, count(*) AS delta
                    FROM new_rows GROUP BY 1,2 ORDER BY 1,2
        LOOP
            PERFORM adjust_telemetry_usage(TG_TABLE_NAME, item.project_id, item.bucket, item.delta);
        END LOOP;
    ELSE
        FOR item IN SELECT project_id, date_trunc('hour', received_at, 'UTC') AS bucket, -count(*) AS delta
                    FROM old_rows GROUP BY 1,2 ORDER BY 1,2
        LOOP
            PERFORM adjust_telemetry_usage(TG_TABLE_NAME, item.project_id, item.bucket, item.delta);
        END LOOP;
    END IF;
    RETURN NULL;
END;
$$;

DO $$
DECLARE relation text;
BEGIN
    FOREACH relation IN ARRAY ARRAY['events','transactions','logs'] LOOP
        EXECUTE format('INSERT INTO telemetry_usage SELECT %L, project_id, date_trunc(''hour'', received_at, ''UTC''), count(*) FROM %I GROUP BY 2,3', relation, relation);
        EXECUTE format('CREATE TRIGGER usage_insert AFTER INSERT ON %I REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION track_telemetry_usage()', relation);
        EXECUTE format('CREATE TRIGGER usage_delete AFTER DELETE ON %I REFERENCING OLD TABLE AS old_rows FOR EACH STATEMENT EXECUTE FUNCTION track_telemetry_usage()', relation);
        EXECUTE format('CREATE TRIGGER usage_update AFTER UPDATE ON %I REFERENCING OLD TABLE AS old_rows NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION track_telemetry_usage()', relation);
        EXECUTE format('CREATE TRIGGER usage_truncate AFTER TRUNCATE ON %I FOR EACH STATEMENT EXECUTE FUNCTION track_telemetry_usage()', relation);
    END LOOP;
END;
$$;

-- Complete UTC hours use counters; partial boundary hours use exact raw rows.
CREATE FUNCTION telemetry_usage_since(cutoff timestamptz DEFAULT NULL, inclusive boolean DEFAULT true, projects uuid[] DEFAULT NULL)
RETURNS TABLE(project_id uuid, kind text, n bigint) LANGUAGE sql STABLE AS $$
    WITH boundary AS (
        SELECT CASE WHEN inclusive AND cutoff = date_trunc('hour', cutoff, 'UTC')
                    THEN cutoff ELSE date_trunc('hour', cutoff, 'UTC') + interval '1 hour' END AS full_start
    ), counts AS (
        SELECT u.project_id, u.kind, u.n FROM telemetry_usage u, boundary b
        WHERE (cutoff IS NULL OR u.bucket >= b.full_start)
          AND (projects IS NULL OR u.project_id = ANY(projects))
        UNION ALL
        SELECT e.project_id, 'events', count(*) FROM events e, boundary b
        WHERE cutoff IS NOT NULL AND e.received_at >= cutoff AND (inclusive OR e.received_at > cutoff)
          AND e.received_at < b.full_start AND (projects IS NULL OR e.project_id = ANY(projects)) GROUP BY e.project_id
        UNION ALL
        SELECT e.project_id, 'transactions', count(*) FROM transactions e, boundary b
        WHERE cutoff IS NOT NULL AND e.received_at >= cutoff AND (inclusive OR e.received_at > cutoff)
          AND e.received_at < b.full_start AND (projects IS NULL OR e.project_id = ANY(projects)) GROUP BY e.project_id
        UNION ALL
        SELECT e.project_id, 'logs', count(*) FROM logs e, boundary b
        WHERE cutoff IS NOT NULL AND e.received_at >= cutoff AND (inclusive OR e.received_at > cutoff)
          AND e.received_at < b.full_start AND (projects IS NULL OR e.project_id = ANY(projects)) GROUP BY e.project_id
    ) SELECT counts.project_id, counts.kind, sum(counts.n)::bigint FROM counts GROUP BY 1,2;
$$;
COMMIT;
