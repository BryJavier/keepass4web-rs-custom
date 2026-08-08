begin;

select plan(33);

select has_table('public', 'vaults', 'vault metadata is stored in public.vaults');
select has_column('public', 'vaults', 'owner_id', 'vaults record their owner');
select has_column('public', 'vaults', 'object_path', 'vaults record the private object path');
select is(
  (select relrowsecurity from pg_class where oid = 'public.vaults'::regclass),
  true,
  'vault metadata is protected by RLS'
);
select is(
  (select public from storage.buckets where id = 'vaults'),
  false,
  'the vault object bucket is private'
);

-- Fixed identities make the ownership boundary deterministic and do not rely on
-- any real account or credential.
insert into auth.users (
  instance_id, id, aud, role, email, encrypted_password, email_confirmed_at,
  raw_app_meta_data, raw_user_meta_data, created_at, updated_at
) values
  (
    '00000000-0000-0000-0000-000000000001',
    '11111111-1111-1111-1111-111111111111',
    'authenticated', 'authenticated', 'vault-owner-a@example.test', '', now(),
    '{"provider":"email","providers":["email"]}', '{}', now(), now()
  ),
  (
    '00000000-0000-0000-0000-000000000001',
    '22222222-2222-2222-2222-222222222222',
    'authenticated', 'authenticated', 'vault-owner-b@example.test', '', now(),
    '{"provider":"email","providers":["email"]}', '{}', now(), now()
  );

set local role authenticated;
select set_config('request.jwt.claim.sub', '11111111-1111-1111-1111-111111111111', true);

insert into public.vaults (id, owner_id, name, object_path)
values (
  'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa',
  '11111111-1111-1111-1111-111111111111',
  'Owner A vault',
  '11111111-1111-1111-1111-111111111111/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa.kdbx'
);

insert into storage.objects (bucket_id, name, owner_id)
values (
  'vaults',
  '11111111-1111-1111-1111-111111111111/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa.kdbx',
  '11111111-1111-1111-1111-111111111111'
);

select set_config('request.jwt.claim.sub', '22222222-2222-2222-2222-222222222222', true);

select is((select count(*) from public.vaults)::integer, 0, 'a second user cannot list another owner''s vaults');
select is((select count(*) from public.vaults where id = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa')::integer, 0, 'a second user cannot select another owner''s vault');
select is_empty(
  $$update public.vaults set name = 'tampered'
    where id = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'
    returning id$$,
  'a second user cannot update another owner''s vault'
);
select is_empty(
  $$delete from public.vaults
    where id = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'
    returning id$$,
  'a second user cannot delete another owner''s vault'
);
select is((select count(*) from storage.objects where bucket_id = 'vaults')::integer, 0, 'a second user cannot download another owner''s vault object');
select is_empty(
  $$update storage.objects set metadata = '{"tampered":true}'::jsonb
    where bucket_id = 'vaults' and name = '11111111-1111-1111-1111-111111111111/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa.kdbx'
    returning name$$,
  'a second user cannot update another owner''s vault object'
);
select throws_ok(
  $$delete from storage.objects where bucket_id = 'vaults' and name = '11111111-1111-1111-1111-111111111111/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa.kdbx'$$,
  '42501',
  'Direct deletion from storage tables is not allowed. Use the Storage API instead.',
  'a second user cannot delete another owner''s vault object'
);
select throws_ok(
  $$insert into storage.objects (bucket_id, name, owner_id) values ('vaults', '11111111-1111-1111-1111-111111111111/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb.kdbx', '22222222-2222-2222-2222-222222222222')$$,
  '42501',
  null,
  'a second user cannot upload into another owner''s object prefix'
);

select set_config('request.jwt.claim.sub', '11111111-1111-1111-1111-111111111111', true);

select is(
  (select name from public.vaults where id = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'),
  'Owner A vault',
  'a second user''s metadata update did not change the vault'
);

select is(
  (select count(*) from storage.objects where bucket_id = 'vaults' and name = '11111111-1111-1111-1111-111111111111/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa.kdbx')::integer,
  1,
  'a second user''s deletion attempt did not remove the vault object'
);

select is(
  (select count(*) from storage.objects where bucket_id = 'vaults' and name = '11111111-1111-1111-1111-111111111111/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa.kdbx' and metadata @> '{"tampered":true}'::jsonb)::integer,
  0,
  'a second user''s object update did not change object metadata'
);

select throws_ok(
  $$insert into storage.objects (bucket_id, name, owner_id) values ('vaults', '11111111-1111-1111-1111-111111111111/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb.kdbx', '11111111-1111-1111-1111-111111111111')$$,
  '42501',
  null,
  'an owner cannot upload an object without matching vault metadata'
);

select is(
  (select count(*) from storage.objects where bucket_id = 'vaults' and name = '11111111-1111-1111-1111-111111111111/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb.kdbx')::integer,
  0,
  'the rejected untracked object was not created'
);

insert into public.vaults (id, owner_id, name, object_path)
values (
  'cccccccc-cccc-cccc-cccc-cccccccccccc',
  '11111111-1111-1111-1111-111111111111',
  'Pending vault',
  '11111111-1111-1111-1111-111111111111/cccccccc-cccc-cccc-cccc-cccccccccccc.kdbx'
);

delete from public.vaults
where id = 'cccccccc-cccc-cccc-cccc-cccccccccccc';

select is(
  (select count(*) from public.vaults where id = 'cccccccc-cccc-cccc-cccc-cccccccccccc')::integer,
  0,
  'metadata without a Storage object can be deleted'
);

select throws_ok(
  $$delete from public.vaults where id = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'$$,
  '23503',
  'Remove the associated Storage object through the Storage API before deleting vault metadata.',
  'vault metadata cannot be deleted while its Storage object exists'
);

select has_column('public', 'vaults', 'lifecycle_state', 'vaults have an explicit lifecycle state');
select has_column('public', 'vaults', 'revision', 'vaults have a revision fencing token');
select has_column('public', 'decoded_vault_entries', 'lifecycle_state', 'decoded entries have an explicit lifecycle state');
select has_column('public', 'decoded_vault_entries', 'transition_state', 'decoded entries have a retryable transition state');

select is(
  (select lifecycle_state from public.vaults where id = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'),
  'active',
  'new vaults begin active'
);
select is(
  (select revision from public.vaults where id = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'),
  1::bigint,
  'new vaults begin at revision one'
);

select throws_ok(
  $$update public.vaults
    set lifecycle_state = 'trashed', trashed_at = now(), purge_after = null
    where id = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'$$,
  '23514', null,
  'a trashed vault requires a purge deadline'
);

insert into public.decoded_vault_entries (vault_id, owner_id, entry_id, group_id, title)
values (
  'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa',
  '11111111-1111-1111-1111-111111111111',
  'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb',
  'cccccccc-cccc-cccc-cccc-cccccccccccc',
  'Lifecycle entry'
);

select throws_ok(
  $$update public.decoded_vault_entries
    set lifecycle_state = 'trashed', trashed_at = now(), purge_after = null
    where vault_id = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'
      and entry_id = 'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb'$$,
  '23514', null,
  'a trashed decoded entry requires a purge deadline'
);

update public.vaults
set lifecycle_state = 'trashed',
    trashed_at = timezone('utc', now()),
    purge_after = timezone('utc', now()) + interval '30 days'
where id = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa';

select is(
  (select revision from public.vaults where id = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'),
  2::bigint,
  'a trash transition increments the vault revision'
);
select is_empty(
  $$update public.decoded_vault_entries set title = 'blocked while parent is trashed'
    where vault_id = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'
      and entry_id = 'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb'
    returning entry_id$$,
  'entry mutation is blocked while its parent vault is trashed'
);

update public.vaults
set lifecycle_state = 'purging'
where id = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa';

select is_empty(
  $$update public.vaults
    set lifecycle_state = 'active', trashed_at = null, purge_after = null
    where id = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'
      and lifecycle_state = 'trashed'
    returning id$$,
  'a purging vault cannot be restored through the trashed transition'
);
select throws_ok(
  $$update public.vaults
    set lifecycle_state = 'active', trashed_at = null, purge_after = null
    where id = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa'$$,
  '23514',
  'Invalid vault lifecycle transition.',
  'the database rejects a direct purging-to-active transition'
);

reset role;

select is(
  (select count(*) from storage.objects where bucket_id = 'vaults' and name = '11111111-1111-1111-1111-111111111111/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa.kdbx')::integer,
  1,
  'a rejected metadata deletion leaves its Storage object intact'
);

select is(
  (select count(*) from public.vaults where id = 'aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa')::integer,
  1,
  'a rejected metadata deletion leaves the vault record intact'
);

select * from finish();
rollback;
