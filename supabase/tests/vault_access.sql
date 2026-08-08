begin;

select plan(21);

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
