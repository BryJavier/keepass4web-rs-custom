-- A vault row is the authority for an object key. Keep storage and metadata
-- in lockstep so a metadata delete cannot leave an inaccessible object behind.
create or replace function public.delete_vault_storage_object()
returns trigger
language plpgsql
security definer
set search_path = ''
as $$
begin
  if exists (
    select 1
    from storage.objects
    where bucket_id = 'vaults'
      and name = old.object_path
  ) then
    raise exception using
      errcode = '23503',
      message = 'Remove the associated Storage object through the Storage API before deleting vault metadata.';
  end if;

  return old;
end;
$$;

revoke all on function public.delete_vault_storage_object() from public;

drop trigger if exists vaults_delete_storage_object on public.vaults;

create trigger vaults_delete_storage_object
before delete on public.vaults
for each row execute function public.delete_vault_storage_object();

drop policy if exists "vault owners can read their objects" on storage.objects;
drop policy if exists "vault owners can upload their objects" on storage.objects;
drop policy if exists "vault owners can update their objects" on storage.objects;
drop policy if exists "vault owners can delete their objects" on storage.objects;
drop policy if exists "vault owners can read their tracked objects" on storage.objects;
drop policy if exists "vault owners can upload their tracked objects" on storage.objects;
drop policy if exists "vault owners can update their tracked objects" on storage.objects;
drop policy if exists "vault owners can delete their tracked objects" on storage.objects;

-- The vaults.object_path check makes this an exact
-- <authenticated owner UUID>/<vault UUID>.kdbx match.  Requiring the
-- metadata row for every storage action prevents unattached objects.
create policy "vault owners can read their tracked objects"
on storage.objects
for select to authenticated
using (
  bucket_id = 'vaults'
  and owner_id = (select auth.uid()::text)
  and exists (
    select 1
    from public.vaults as vault
    where vault.owner_id = (select auth.uid())
      and vault.object_path = storage.objects.name
  )
);

create policy "vault owners can upload their tracked objects"
on storage.objects
for insert to authenticated
with check (
  bucket_id = 'vaults'
  and owner_id = (select auth.uid()::text)
  and exists (
    select 1
    from public.vaults as vault
    where vault.owner_id = (select auth.uid())
      and vault.object_path = storage.objects.name
  )
);

create policy "vault owners can update their tracked objects"
on storage.objects
for update to authenticated
using (
  bucket_id = 'vaults'
  and owner_id = (select auth.uid()::text)
  and exists (
    select 1
    from public.vaults as vault
    where vault.owner_id = (select auth.uid())
      and vault.object_path = storage.objects.name
  )
)
with check (
  bucket_id = 'vaults'
  and owner_id = (select auth.uid()::text)
  and exists (
    select 1
    from public.vaults as vault
    where vault.owner_id = (select auth.uid())
      and vault.object_path = storage.objects.name
  )
);

create policy "vault owners can delete their tracked objects"
on storage.objects
for delete to authenticated
using (
  bucket_id = 'vaults'
  and owner_id = (select auth.uid()::text)
  and exists (
    select 1
    from public.vaults as vault
    where vault.owner_id = (select auth.uid())
      and vault.object_path = storage.objects.name
  )
);
