-- Recoverable trash lifecycle. The state machine makes every active/trash
-- transition explicit and lets a server-only worker claim a vault before it
-- removes the object through the Storage API.
alter table public.vaults
  add column trashed_at timestamptz,
  add column purge_after timestamptz,
  add column lifecycle_state text not null default 'active',
  add column revision bigint not null default 1,
  add constraint vaults_lifecycle_state_check
    check (lifecycle_state in ('active', 'trashed', 'purging')),
  add constraint vaults_revision_check
    check (revision > 0),
  add constraint vaults_trash_purge_after_check
    check (
      (lifecycle_state = 'active' and trashed_at is null and purge_after is null)
      or (
        lifecycle_state in ('trashed', 'purging')
        and trashed_at is not null
        and purge_after is not null
        and purge_after = trashed_at + interval '30 days'
      )
    );

alter table public.decoded_vault_entries
  add column trashed_at timestamptz,
  add column purge_after timestamptz,
  add column lifecycle_state text not null default 'active',
  add column transition_state text,
  add constraint decoded_vault_entries_lifecycle_state_check
    check (lifecycle_state in ('active', 'trashed')),
  add constraint decoded_vault_entries_transition_state_check
    check (
      transition_state is null
      or (transition_state = 'trashing' and lifecycle_state = 'active')
      or (transition_state = 'restoring' and lifecycle_state = 'trashed')
    ),
  add constraint decoded_vault_entries_trash_purge_after_check
    check (
      (lifecycle_state = 'active' and trashed_at is null and purge_after is null)
      or (
        lifecycle_state = 'trashed'
        and trashed_at is not null
        and purge_after is not null
        and purge_after = trashed_at + interval '30 days'
      )
    );

-- Every lifecycle transition receives a new revision. The purge worker carries
-- the returned revision into its final metadata delete as a fencing token.
create function public.bump_vault_lifecycle_revision()
returns trigger
language plpgsql
set search_path = ''
as $$
begin
  if new.lifecycle_state is not distinct from old.lifecycle_state then
    if new.lifecycle_state in ('trashed', 'purging')
      and (new.trashed_at is distinct from old.trashed_at
        or new.purge_after is distinct from old.purge_after) then
      raise exception using errcode = '23514',
        message = 'A vault tombstone retention deadline is immutable.';
    end if;
    new.revision = old.revision;
    return new;
  end if;

  if not (
    (old.lifecycle_state = 'active' and new.lifecycle_state = 'trashed')
    or (old.lifecycle_state = 'trashed' and new.lifecycle_state in ('active', 'purging'))
  ) then
    raise exception using errcode = '23514',
      message = 'Invalid vault lifecycle transition.';
  end if;

  new.revision = old.revision + 1;
  return new;
end;
$$;

revoke all on function public.bump_vault_lifecycle_revision() from public;

create trigger vaults_bump_lifecycle_revision
before update on public.vaults
for each row execute function public.bump_vault_lifecycle_revision();

create index vaults_owner_id_lifecycle_state_idx
  on public.vaults (owner_id, lifecycle_state, created_at desc);

create index vaults_purge_after_idx
  on public.vaults (purge_after)
  where lifecycle_state in ('trashed', 'purging');

create index decoded_vault_entries_owner_id_lifecycle_state_idx
  on public.decoded_vault_entries (owner_id, vault_id, lifecycle_state, title);

create index decoded_vault_entries_purge_after_idx
  on public.decoded_vault_entries (purge_after)
  where lifecycle_state = 'trashed' and transition_state is null;

-- An authenticated owner may only operate on decoded entries while the parent
-- vault remains active. This mirrors the repository's parent join filters.
drop policy "decoded entry owners can insert" on public.decoded_vault_entries;
create policy "decoded entry owners can insert"
on public.decoded_vault_entries for insert to authenticated
with check (
  owner_id = (select auth.uid())
  and exists (
    select 1 from public.vaults v
    where v.id = vault_id
      and v.owner_id = (select auth.uid())
      and v.lifecycle_state = 'active'
  )
);

drop policy "decoded entry owners can update" on public.decoded_vault_entries;
create policy "decoded entry owners can update"
on public.decoded_vault_entries for update to authenticated
using (
  owner_id = (select auth.uid())
  and exists (
    select 1 from public.vaults v
    where v.id = vault_id
      and v.owner_id = (select auth.uid())
      and v.lifecycle_state = 'active'
  )
)
with check (
  owner_id = (select auth.uid())
  and exists (
    select 1 from public.vaults v
    where v.id = vault_id
      and v.owner_id = (select auth.uid())
      and v.lifecycle_state = 'active'
  )
);

drop policy "decoded entry owners can delete" on public.decoded_vault_entries;
create policy "decoded entry owners can delete"
on public.decoded_vault_entries for delete to authenticated
using (
  owner_id = (select auth.uid())
  and exists (
    select 1 from public.vaults v
    where v.id = vault_id
      and v.owner_id = (select auth.uid())
      and v.lifecycle_state = 'active'
  )
);

-- Storage is unavailable to a trashed/purging vault even when its object path
-- is known. The server-only purger bypasses RLS but still uses Storage HTTP.
drop policy "vault owners can read their tracked objects" on storage.objects;
create policy "vault owners can read their tracked objects"
on storage.objects for select to authenticated
using (
  bucket_id = 'vaults'
  and owner_id = (select auth.uid()::text)
  and exists (
    select 1 from public.vaults vault
    where vault.owner_id = (select auth.uid())
      and vault.object_path = storage.objects.name
      and vault.lifecycle_state = 'active'
  )
);

drop policy "vault owners can upload their tracked objects" on storage.objects;
create policy "vault owners can upload their tracked objects"
on storage.objects for insert to authenticated
with check (
  bucket_id = 'vaults'
  and owner_id = (select auth.uid()::text)
  and exists (
    select 1 from public.vaults vault
    where vault.owner_id = (select auth.uid())
      and vault.object_path = storage.objects.name
      and vault.lifecycle_state = 'active'
  )
);

drop policy "vault owners can update their tracked objects" on storage.objects;
create policy "vault owners can update their tracked objects"
on storage.objects for update to authenticated
using (
  bucket_id = 'vaults'
  and owner_id = (select auth.uid()::text)
  and exists (
    select 1 from public.vaults vault
    where vault.owner_id = (select auth.uid())
      and vault.object_path = storage.objects.name
      and vault.lifecycle_state = 'active'
  )
)
with check (
  bucket_id = 'vaults'
  and owner_id = (select auth.uid()::text)
  and exists (
    select 1 from public.vaults vault
    where vault.owner_id = (select auth.uid())
      and vault.object_path = storage.objects.name
      and vault.lifecycle_state = 'active'
  )
);

drop policy "vault owners can delete their tracked objects" on storage.objects;
create policy "vault owners can delete their tracked objects"
on storage.objects for delete to authenticated
using (
  bucket_id = 'vaults'
  and owner_id = (select auth.uid()::text)
  and exists (
    select 1 from public.vaults vault
    where vault.owner_id = (select auth.uid())
      and vault.object_path = storage.objects.name
      and vault.lifecycle_state = 'active'
  )
);
