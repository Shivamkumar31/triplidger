# Suggested next phases

1. Run the starter and smoke test against a Neon development branch. Learn the transaction flow.
2. Add cursor pagination to expenses and settlements before expanding beyond small groups.
3. Add revision-history endpoints/UI and audited expense voiding; retain confirmed repayments.
4. Add Redis cache-aside summaries with versioned keys and PostgreSQL fallback.
5. Add distributed Redis rate limiting, authentication recovery and session cleanup.
6. Add PostgreSQL outbox + worker for retryable report generation; jobs must be idempotent.
7. Add private receipt uploads, size/type validation, authorized downloads and CDN signed URLs.
8. Add automated CI with a separate ephemeral Neon test branch, metrics and realistic load tests.

Keep PostgreSQL authoritative. Do not turn Redis into a second financial database.
Do not add payment-gateway processing until the accounting and confirmation flow is reliable.
