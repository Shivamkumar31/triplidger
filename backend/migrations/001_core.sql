-- Compatible with the seven tables previously supplied in chat.
-- IF NOT EXISTS preserves existing data; it does not upgrade differing schemas.
CREATE TABLE IF NOT EXISTS public.users (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), name TEXT NOT NULL CHECK(length(trim(name)) BETWEEN 1 AND 100),
 email TEXT NOT NULL UNIQUE CHECK(email=lower(trim(email)) AND length(email) BETWEEN 3 AND 254),
 password_hash TEXT NOT NULL CHECK(length(password_hash)>0), created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS public.groups (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), name TEXT NOT NULL CHECK(length(trim(name)) BETWEEN 1 AND 100),
 currency TEXT NOT NULL DEFAULT 'INR' CHECK(currency='INR'), created_by UUID NOT NULL REFERENCES public.users(id),
 financial_version BIGINT NOT NULL DEFAULT 0 CHECK(financial_version>=0), archived_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS public.group_members (
 group_id UUID NOT NULL REFERENCES public.groups(id), user_id UUID NOT NULL REFERENCES public.users(id),
 role TEXT NOT NULL DEFAULT 'member' CHECK(role IN ('owner','admin','member')),
 status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','left')), joined_at TIMESTAMPTZ NOT NULL DEFAULT now(), left_at TIMESTAMPTZ,
 PRIMARY KEY(group_id,user_id),
 CHECK((status='active' AND left_at IS NULL) OR (status='left' AND left_at IS NOT NULL)), CHECK(left_at IS NULL OR left_at>=joined_at)
);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_owner_per_group ON public.group_members(group_id) WHERE role='owner' AND status='active';
CREATE INDEX IF NOT EXISTS group_members_user_idx ON public.group_members(user_id,status);
CREATE TABLE IF NOT EXISTS public.group_invites (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), group_id UUID NOT NULL REFERENCES public.groups(id), created_by UUID NOT NULL,
 token_hash TEXT NOT NULL UNIQUE CHECK(length(token_hash)>0), expires_at TIMESTAMPTZ NOT NULL, revoked_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(group_id,created_by) REFERENCES public.group_members(group_id,user_id), CHECK(expires_at>created_at), CHECK(revoked_at IS NULL OR revoked_at>=created_at)
);
CREATE INDEX IF NOT EXISTS group_invites_group_idx ON public.group_invites(group_id);
CREATE TABLE IF NOT EXISTS public.expenses (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), group_id UUID NOT NULL REFERENCES public.groups(id), paid_by UUID NOT NULL, created_by UUID NOT NULL,
 description TEXT NOT NULL CHECK(length(trim(description)) BETWEEN 1 AND 500), amount_paise BIGINT NOT NULL CHECK(amount_paise>0),
 split_type TEXT NOT NULL DEFAULT 'equal' CHECK(split_type='equal'), occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 version BIGINT NOT NULL DEFAULT 1 CHECK(version>=1), voided_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(group_id,paid_by) REFERENCES public.group_members(group_id,user_id), FOREIGN KEY(group_id,created_by) REFERENCES public.group_members(group_id,user_id), UNIQUE(id,group_id)
);
CREATE INDEX IF NOT EXISTS expenses_group_created_idx ON public.expenses(group_id,created_at DESC,id DESC);
CREATE TABLE IF NOT EXISTS public.expense_splits (
 expense_id UUID NOT NULL, group_id UUID NOT NULL, user_id UUID NOT NULL, amount_paise BIGINT NOT NULL CHECK(amount_paise>=0),
 PRIMARY KEY(expense_id,user_id), FOREIGN KEY(expense_id,group_id) REFERENCES public.expenses(id,group_id),
 FOREIGN KEY(group_id,user_id) REFERENCES public.group_members(group_id,user_id)
);
CREATE INDEX IF NOT EXISTS expense_splits_group_user_idx ON public.expense_splits(group_id,user_id);
CREATE TABLE IF NOT EXISTS public.settlements (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), group_id UUID NOT NULL REFERENCES public.groups(id), from_user_id UUID NOT NULL, to_user_id UUID NOT NULL,
 amount_paise BIGINT NOT NULL CHECK(amount_paise>0), status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','confirmed','rejected','cancelled')),
 confirmed_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 FOREIGN KEY(group_id,from_user_id) REFERENCES public.group_members(group_id,user_id), FOREIGN KEY(group_id,to_user_id) REFERENCES public.group_members(group_id,user_id),
 CHECK(from_user_id<>to_user_id), CHECK((status='confirmed' AND confirmed_at IS NOT NULL) OR (status<>'confirmed' AND confirmed_at IS NULL)), CHECK(confirmed_at IS NULL OR confirmed_at>=created_at)
);
CREATE INDEX IF NOT EXISTS settlements_group_status_idx ON public.settlements(group_id,status);
