# API contract
All business endpoints have the `/api` prefix. Browser requests go to localhost:3000.
Authenticated endpoints require the session cookie. Every mutation requires:
`Origin: http://localhost:3000` and `X-TripLedger: 1`.
Expense creation/editing and repayment creation also require `Idempotency-Key` (8–100 characters).
Use a new UUID per logical action and reuse it for retries of that same action.
Errors are JSON: `{"error":"message"}`. No stack traces/SQL are returned.

| Method | Path | Body |
|---|---|---|
| POST | /auth/register | name, email, password (10–72 bytes) |
| POST | /auth/login | email, password |
| POST | /auth/logout | {} |
| GET | /me | — |
| GET | /groups | — |
| POST | /groups | name |
| POST | /groups/{gid}/invites | {} (owner/admin only) |
| POST | /invites/accept | token (raw 64-char invite token) |
| GET | /groups/{gid}/dashboard | — |
| POST | /groups/{gid}/expenses | description, amount_paise, paid_by, participant_ids, version:0 |
| PATCH | /groups/{gid}/expenses/{eid} | same fields; current version required |
| POST | /groups/{gid}/settlements | to_user_id, amount_paise |
| POST | /groups/{gid}/settlements/{sid}/action | action: confirm, reject, or cancel |

The sender of a repayment is always the session user, never an arbitrary body field.
Any active member may record an expense paid by another active member. The creator or
owner/admin may edit an expense. Only the recipient may confirm/reject; only the sender may cancel.
Confirmed/rejected/cancelled repayments cannot transition to another state. A repeated identical
action returns the existing state, without applying another financial adjustment.

Dashboard returns: id, name, role, financial_version, total_paise, members, expenses,
settlements, suggestions. Members include paid_paise, share_paise, balance_paise.
Suggestions are not persisted payments.

## Status codes
200/201 success; 400 invalid input; 401 sign in required; 403 missing permissions/origin;
404 record missing; 409 stale version, uniqueness or idempotency conflict; 429 auth rate limit;
500 generic internal failure. /healthz is a non-authenticated database readiness check (503 on failure).
