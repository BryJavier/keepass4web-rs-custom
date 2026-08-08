-- Private multi-vault foundation.  Vault bytes remain in Storage; this table
-- contains only metadata and a deterministic, owner-scoped object path.
create extension if not exists pgcrypto with schema extensions;

create table public.vaults (
  id uuid primary key default gen_random_uuid(),
  owner_id uuid not null references auth.users (id) on delete cascade,
  name text not null check (char_length(btrim(name)) between 1 and 255),
  object_path text not null unique,
  upload_state text not null default 'pending'
    check (upload_state in ('pending', 'uploaded', 'failed')),
  created_at timestamptz not null default timezone('utc', now()),
  updated_at timestamptz not null default timezone('utc', now()),
  constraint vaults_object_path_is_owner_scoped
    check (object_path = owner_id::text || '/' || id::text || '.kdbx')
);

comment on table public.vaults is
  'Non-sensitive metadata for encrypted KeePass database objects in private Storage.';
comment on column public.vaults.object_path is
  'Private Storage key: <owner UUID>/<vault UUID>.kdbx.';

create index vaults_owner_id_created_at_idx on public.vaults (owner_id, created_at desc);

create function public.set_vaults_updated_at()
returns trigger
language plpgsql
set search_path = ''
as $$
begin
  new.updated_at = timezone('utc', now());
  return new;
end;
$$;

create trigger vaults_set_updated_at
before update on public.vaults
for each row execute function public.set_vaults_updated_at();

alter table public.vaults enable row level security;
alter table public.vaults force row level security;

grant select, insert, update, delete on public.vaults to authenticated;

create policy "vault owners can select their metadata"
on public.vaults
for select to authenticated
using (owner_id = (select auth.uid()));

create policy "vault owners can create their metadata"
on public.vaults
for insert to authenticated
with check (
  owner_id = (select auth.uid())
  and object_path = owner_id::text || '/' || id::text || '.kdbx'
);

create policy "vault owners can update their metadata"
on public.vaults
for update to authenticated
using (owner_id = (select auth.uid()))
with check (
  owner_id = (select auth.uid())
  and object_path = owner_id::text || '/' || id::text || '.kdbx'
);

create policy "vault owners can delete their metadata"
on public.vaults
for delete to authenticated
using (owner_id = (select auth.uid()));

-- Storage objects are private even when their path is guessed.  The exact
-- filename shape prevents nested prefixes and keeps keys compatible with the
-- vaults.object_path constraint.
insert into storage.buckets (id, name, public, file_size_limit)
values ('vaults', 'vaults', false, 52428800)
on conflict (id) do update
set public = false,
    file_size_limit = excluded.file_size_limit;

create policy "vault owners can read their objects"
on storage.objects
for select to authenticated
using (
  bucket_id = 'vaults'
  and (storage.foldername(name))[1] = (select auth.uid()::text)
  and name ~* ('^' || (select auth.uid()::text) || '/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.kdbx$')
);

create policy "vault owners can upload their objects"
on storage.objects
for insert to authenticated
with check (
  bucket_id = 'vaults'
  and (storage.foldername(name))[1] = (select auth.uid()::text)
  and name ~* ('^' || (select auth.uid()::text) || '/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.kdbx$')
);

create policy "vault owners can update their objects"
on storage.objects
for update to authenticated
using (
  bucket_id = 'vaults'
  and (storage.foldername(name))[1] = (select auth.uid()::text)
  and name ~* ('^' || (select auth.uid()::text) || '/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.kdbx$')
)
with check (
  bucket_id = 'vaults'
  and (storage.foldername(name))[1] = (select auth.uid()::text)
  and name ~* ('^' || (select auth.uid()::text) || '/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.kdbx$')
);

create policy "vault owners can delete their objects"
on storage.objects
for delete to authenticated
using (
  bucket_id = 'vaults'
  and (storage.foldername(name))[1] = (select auth.uid()::text)
  and name ~* ('^' || (select auth.uid()::text) || '/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.kdbx$')
);
