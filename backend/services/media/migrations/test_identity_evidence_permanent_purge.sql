DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema='public'
      AND table_name='media_objects'
      AND column_name='identity_evidence_purged_at'
      AND data_type='timestamp with time zone'
  ) THEN
    RAISE EXCEPTION 'identity_evidence_purged_at migration column is missing or has the wrong type';
  END IF;

  IF NOT EXISTS (
    SELECT 1
    FROM pg_description d
    JOIN pg_class c ON c.oid=d.objoid
    JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum=d.objsubid
    WHERE c.relname='media_objects'
      AND a.attname='identity_evidence_purged_at'
      AND d.description ILIKE '%every Backblaze B2 object version and delete marker%'
  ) THEN
    RAISE EXCEPTION 'permanent B2 purge semantics are not documented on the state marker';
  END IF;
END $$;
