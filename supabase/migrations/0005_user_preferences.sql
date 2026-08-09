-- Per-user server-side preferences. Currently holds only the dark-mode
-- choice; NULL/absent means "follow system", matching the client's
-- prefers-color-scheme fallback in app.js's applyInitialTheme().
create table public.user_preferences (
  user_id uuid primary key references auth.users (id) on delete cascade,
  theme_preference text check (theme_preference in ('light', 'dark')),
  updated_at timestamptz not null default timezone('utc', now())
);

comment on table public.user_preferences is
  'Per-user UI preferences, currently just theme_preference.';

create function public.set_user_preferences_updated_at()
returns trigger
language plpgsql
set search_path = ''
as $$
begin
  new.updated_at = timezone('utc', now());
  return new;
end;
$$;

create trigger user_preferences_set_updated_at
before update on public.user_preferences
for each row execute function public.set_user_preferences_updated_at();

alter table public.user_preferences enable row level security;
alter table public.user_preferences force row level security;

grant select, insert, update on public.user_preferences to authenticated;

create policy "users can select their preferences"
on public.user_preferences
for select to authenticated
using (user_id = (select auth.uid()));

create policy "users can create their preferences"
on public.user_preferences
for insert to authenticated
with check (user_id = (select auth.uid()));

create policy "users can update their preferences"
on public.user_preferences
for update to authenticated
using (user_id = (select auth.uid()))
with check (user_id = (select auth.uid()));
