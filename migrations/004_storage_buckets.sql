do $$
begin
  -- The plain PostgreSQL CI database has no Supabase Storage schema. On the
  -- real VPS, official Supabase creates storage.buckets/objects before the
  -- Testagram migrations run, so apply the bucket/policy configuration there.
  if to_regclass('storage.buckets') is null or to_regclass('storage.objects') is null then
    raise notice 'Supabase Storage schema not present; skipping storage bucket provisioning';
    return;
  end if;

  insert into storage.buckets (id,name,public,file_size_limit)
  values
    ('post-media','post-media',true,21474836480),
    ('avatars','avatars',true,104857600),
    ('stories','stories',true,5368709120),
    ('attachments','attachments',false,21474836480)
  on conflict (id) do update
    set public=excluded.public,file_size_limit=excluded.file_size_limit;

  execute 'drop policy if exists "authenticated media upload" on storage.objects';
  execute $policy$
    create policy "authenticated media upload"
    on storage.objects for insert to authenticated
    with check (
      bucket_id in ('post-media','avatars','stories','attachments')
      and (storage.foldername(name))[1] = (select auth.uid()::text)
    )
  $policy$;

  execute 'drop policy if exists "authenticated media update own" on storage.objects';
  execute $policy$
    create policy "authenticated media update own"
    on storage.objects for update to authenticated
    using (
      bucket_id in ('post-media','avatars','stories','attachments')
      and owner_id = (select auth.uid())
    )
    with check (
      bucket_id in ('post-media','avatars','stories','attachments')
      and owner_id = (select auth.uid())
    )
  $policy$;

  execute 'drop policy if exists "authenticated media delete own" on storage.objects';
  execute $policy$
    create policy "authenticated media delete own"
    on storage.objects for delete to authenticated
    using (
      bucket_id in ('post-media','avatars','stories','attachments')
      and owner_id = (select auth.uid())
    )
  $policy$;
end $$;
