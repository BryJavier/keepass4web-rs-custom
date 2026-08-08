-- Recoverable trash lifecycle. Purge workers use these metadata timestamps
-- with server-only credentials and remove Storage objects through the Storage
-- API before deleting the corresponding metadata rows.
alter table public.vaults
  add column trashed_at timestamptz,
  add column purge_after timestamptz,
  add constraint vaults_trash_purge_after_check
    check (
      (trashed_at is null and purge_after is null)
      or (
        trashed_at is not null
        and purge_after is not null
        and purge_after = trashed_at + interval '30 days'
      )
    );

alter table public.decoded_vault_entries
  add column trashed_at timestamptz,
  add column purge_after timestamptz,
  add constraint decoded_vault_entries_trash_purge_after_check
    check (
      (trashed_at is null and purge_after is null)
      or (
        trashed_at is not null
        and purge_after is not null
        and purge_after = trashed_at + interval '30 days'
      )
    );

create index vaults_owner_id_trashed_at_idx
  on public.vaults (owner_id, trashed_at);

create index vaults_purge_after_idx
  on public.vaults (purge_after)
  where purge_after is not null;

create index decoded_vault_entries_owner_id_trashed_at_idx
  on public.decoded_vault_entries (owner_id, trashed_at);

create index decoded_vault_entries_purge_after_idx
  on public.decoded_vault_entries (purge_after)
  where purge_after is not null;

