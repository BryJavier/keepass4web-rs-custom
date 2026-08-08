-- Decoded KeePass entries mirror. This intentionally stores the decoded
-- fields requested by the owner, including password values, and is protected
-- by owner-only RLS.
create table public.decoded_vault_entries (
  vault_id uuid not null references public.vaults(id) on delete cascade,
  owner_id uuid not null references auth.users(id) on delete cascade,
  entry_id uuid not null,
  group_id uuid not null,
  title text not null default '',
  username text not null default '',
  password text not null default '',
  url text not null default '',
  notes text not null default '',
  fields jsonb not null default '{}'::jsonb,
  created_at timestamptz not null default timezone('utc', now()),
  updated_at timestamptz not null default timezone('utc', now()),
  primary key (vault_id, entry_id)
);

create index decoded_vault_entries_owner_vault_idx
  on public.decoded_vault_entries (owner_id, vault_id, title);

create trigger decoded_vault_entries_set_updated_at
before update on public.decoded_vault_entries
for each row execute function public.set_vaults_updated_at();

alter table public.decoded_vault_entries enable row level security;
alter table public.decoded_vault_entries force row level security;
grant select, insert, update, delete on public.decoded_vault_entries to authenticated;

create policy "decoded entry owners can select"
on public.decoded_vault_entries for select to authenticated
using (owner_id = (select auth.uid()));

create policy "decoded entry owners can insert"
on public.decoded_vault_entries for insert to authenticated
with check (
  owner_id = (select auth.uid())
  and exists (select 1 from public.vaults v where v.id = vault_id and v.owner_id = (select auth.uid()))
);

create policy "decoded entry owners can update"
on public.decoded_vault_entries for update to authenticated
using (owner_id = (select auth.uid()))
with check (owner_id = (select auth.uid()));

create policy "decoded entry owners can delete"
on public.decoded_vault_entries for delete to authenticated
using (owner_id = (select auth.uid()));
