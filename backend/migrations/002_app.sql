CREATE TABLE IF NOT EXISTS public.sessions (
 token_hash TEXT PRIMARY KEY, user_id UUID NOT NULL REFERENCES public.users(id), expires_at TIMESTAMPTZ NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS sessions_user_idx ON public.sessions(user_id);
CREATE TABLE IF NOT EXISTS public.idempotency_requests (
 actor_id UUID NOT NULL REFERENCES public.users(id), scope TEXT NOT NULL, key TEXT NOT NULL,
 request_hash TEXT NOT NULL, response JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(actor_id,scope,key)
);
CREATE TABLE IF NOT EXISTS public.expense_revisions (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), expense_id UUID NOT NULL REFERENCES public.expenses(id), actor_id UUID NOT NULL REFERENCES public.users(id),
 old_version BIGINT NOT NULL, snapshot JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(expense_id,old_version)
);
