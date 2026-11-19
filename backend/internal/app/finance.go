package app

import (
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"tripledger/internal/ledger"
)

type ExpenseInput struct {
	Description  string   `json:"description"`
	Amount       int64    `json:"amount_paise"`
	PaidBy       string   `json:"paid_by"`
	Participants []string `json:"participant_ids"`
	Version      int64    `json:"version"`
}

func (a *App) expense(w http.ResponseWriter, r *http.Request) error {
	var in ExpenseInput
	if err := decode(w, r, &in); err != nil {
		return err
	}
	in.Description = strings.TrimSpace(in.Description)
	if len(in.Description) < 1 || len(in.Description) > 500 {
		return bad("Description must be 1–500 bytes")
	}
	shares, err := ledger.Equal(in.Amount, in.Participants)
	if err != nil {
		return bad(err.Error())
	}
	ctx := r.Context()
	gid := r.PathValue("gid")
	tx, role, err := a.groupTx(r)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	scope := r.Method + ":" + r.URL.Path
	previous, hash, err := replay(ctx, tx, r, scope, in)
	if err != nil {
		return err
	}
	if previous != nil {
		send(w, 200, previous)
		return nil
	}
	if err = active(ctx, tx, gid, in.PaidBy); err != nil {
		return err
	}
	for _, s := range shares {
		if err = active(ctx, tx, gid, s.UserID); err != nil {
			return err
		}
	}
	eid := r.PathValue("eid")
	version := int64(1)
	if eid == "" {
		var count int
		if err = tx.QueryRow(ctx, "SELECT count(*) FROM public.expenses WHERE group_id=$1", gid).Scan(&count); err != nil {
			return err
		}
		if count >= 10000 {
			return conflict("Starter supports 10,000 expenses per group")
		}
		if err = tx.QueryRow(ctx, "INSERT INTO public.expenses(group_id,paid_by,created_by,description,amount_paise) VALUES($1,$2,$3,$4,$5) RETURNING id::text", gid, in.PaidBy, actor(r), in.Description, in.Amount).Scan(&eid); err != nil {
			return err
		}
	} else {
		var creator string
		var oldVersion int64
		var snapshot []byte
		err = tx.QueryRow(ctx, `SELECT e.created_by::text,e.version,jsonb_build_object('expense',to_jsonb(e),'splits',COALESCE((SELECT jsonb_agg(to_jsonb(s)) FROM public.expense_splits s WHERE s.expense_id=e.id),'[]'::jsonb)) FROM public.expenses e WHERE e.id=$1 AND e.group_id=$2 AND e.voided_at IS NULL`, eid, gid).Scan(&creator, &oldVersion, &snapshot)
		if err != nil {
			return err
		}
		if creator != actor(r) && role == "member" {
			return denied()
		}
		if oldVersion != in.Version {
			return conflict("Expense changed. Refresh before editing")
		}
		_, err = tx.Exec(ctx, "INSERT INTO public.expense_revisions(expense_id,actor_id,old_version,snapshot) VALUES($1,$2,$3,$4::jsonb)", eid, actor(r), oldVersion, string(snapshot))
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "UPDATE public.expenses SET description=$1,amount_paise=$2,paid_by=$3,version=version+1 WHERE id=$4", in.Description, in.Amount, in.PaidBy, eid); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "DELETE FROM public.expense_splits WHERE expense_id=$1", eid); err != nil {
			return err
		}
		version = oldVersion + 1
	}
	for _, s := range shares {
		if _, err = tx.Exec(ctx, "INSERT INTO public.expense_splits(expense_id,group_id,user_id,amount_paise) VALUES($1,$2,$3,$4)", eid, gid, s.UserID, s.Amount); err != nil {
			return err
		}
	}
	if err = bump(ctx, tx, gid); err != nil {
		return err
	}
	result := map[string]any{"id": eid, "version": version}
	if err = remember(ctx, tx, r, scope, hash, result); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	send(w, 200, result)
	return nil
}
func (a *App) settle(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		To     string `json:"to_user_id"`
		Amount int64  `json:"amount_paise"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	if in.Amount <= 0 || in.Amount > ledger.MaxAmount || in.To == actor(r) {
		return bad("Choose another member and a positive amount within the limit")
	}
	ctx := r.Context()
	tx, _, err := a.groupTx(r)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	scope := r.Method + ":" + r.URL.Path
	previous, hash, err := replay(ctx, tx, r, scope, in)
	if err != nil {
		return err
	}
	if previous != nil {
		send(w, 200, previous)
		return nil
	}
	if err = active(ctx, tx, r.PathValue("gid"), in.To); err != nil {
		return err
	}
	var id string
	err = tx.QueryRow(ctx, "INSERT INTO public.settlements(group_id,from_user_id,to_user_id,amount_paise) VALUES($1,$2,$3,$4) RETURNING id::text", r.PathValue("gid"), actor(r), in.To, in.Amount).Scan(&id)
	if err != nil {
		return err
	}
	if err = bump(ctx, tx, r.PathValue("gid")); err != nil {
		return err
	}
	result := map[string]string{"id": id, "status": "pending"}
	if err = remember(ctx, tx, r, scope, hash, result); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	send(w, 201, result)
	return nil
}
func (a *App) settlementAction(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Action string `json:"action"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	if in.Action != "confirm" && in.Action != "reject" && in.Action != "cancel" {
		return bad("Invalid action")
	}
	ctx := r.Context()
	tx, _, err := a.groupTx(r)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var from, to, status string
	err = tx.QueryRow(ctx, "SELECT from_user_id::text,to_user_id::text,status FROM public.settlements WHERE id=$1 AND group_id=$2", r.PathValue("sid"), r.PathValue("gid")).Scan(&from, &to, &status)
	if err != nil {
		return err
	}
	if (in.Action == "cancel" && from != actor(r)) || (in.Action != "cancel" && to != actor(r)) {
		return denied()
	}
	target := map[string]string{"confirm": "confirmed", "reject": "rejected", "cancel": "cancelled"}[in.Action]
	if status == target {
		send(w, 200, map[string]string{"status": status})
		return nil
	}
	if status != "pending" {
		return conflict("This repayment is no longer pending")
	}
	_, err = tx.Exec(ctx, "UPDATE public.settlements SET status=$1,confirmed_at=CASE WHEN $1='confirmed' THEN now() ELSE NULL END WHERE id=$2", target, r.PathValue("sid"))
	if err != nil {
		return err
	}
	if err = bump(ctx, tx, r.PathValue("gid")); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	send(w, 200, map[string]string{"status": target})
	return nil
}
func (a *App) dashboard(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	dbStart := time.Now()
	var dbDuration time.Duration
	var cacheGetDuration time.Duration
	var cacheSetDuration time.Duration
	setTiming := func() {
		parts := []string{
			"db;dur=" + strconv.FormatFloat(float64(dbDuration.Microseconds())/1000, 'f', 3, 64),
		}
		if cacheGetDuration > 0 {
			parts = append(parts, "cache_get;dur="+strconv.FormatFloat(float64(cacheGetDuration.Microseconds())/1000, 'f', 3, 64))
		}
		if cacheSetDuration > 0 {
			parts = append(parts, "cache_set;dur="+strconv.FormatFloat(float64(cacheSetDuration.Microseconds())/1000, 'f', 3, 64))
		}
		w.Header().Set("Server-Timing", strings.Join(parts, ", "))
	}
	if !a.CacheEnabled {
		w.Header().Set("X-Cache", "BYPASS")
	} else if a.Cache == nil {
		w.Header().Set("X-Cache", "ERROR")
	}
	tx, err := a.DB.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	gid := r.PathValue("gid")
	var name, role string
	var version int64
	err = tx.QueryRow(ctx, `SELECT g.name,g.financial_version,m.role FROM public.groups g JOIN public.group_members m ON m.group_id=g.id WHERE g.id=$1 AND m.user_id=$2 AND m.status='active'`, gid, actor(r)).Scan(&name, &version, &role)
	if err != nil {
		if err == pgx.ErrNoRows {
			dbDuration = time.Since(dbStart)
			setTiming()
			return denied()
		}
		return err
	}
	dbDuration = time.Since(dbStart)
	if a.CacheEnabled && a.Cache != nil {
		cacheStart := time.Now()
		if payload, ok, cacheErr := loadCachedDashboard(a.Cache, ctx, gid, version); cacheErr == nil {
			cacheGetDuration = time.Since(cacheStart)
			if ok {
				payload["role"] = role
				tx.Rollback(ctx)
				cacheLog("hit", gid)
				w.Header().Set("X-Cache", "HIT")
				setTiming()
				send(w, 200, payload)
				return nil
			}
			cacheLog("miss", gid)
			w.Header().Set("X-Cache", "MISS")
		} else if isCacheMiss(cacheErr) {
			cacheGetDuration = time.Since(cacheStart)
			cacheLog("miss", gid)
			w.Header().Set("X-Cache", "MISS")
		} else {
			cacheGetDuration = time.Since(cacheStart)
			slog.Info("dashboard cache error", "group_id", gid)
			w.Header().Set("X-Cache", "ERROR")
		}
	}
	dbPhaseStart := time.Now()
	// UNION ALL prevents the join-multiplication bug when combining shares and repayments.
	rows, err := tx.Query(ctx, `WITH entries AS (
 SELECT paid_by AS user_id, amount_paise AS paid, 0::bigint AS owed, amount_paise AS delta FROM public.expenses WHERE group_id=$1 AND voided_at IS NULL
 UNION ALL SELECT s.user_id,0,s.amount_paise,-s.amount_paise FROM public.expense_splits s JOIN public.expenses e ON e.id=s.expense_id WHERE s.group_id=$1 AND e.voided_at IS NULL
 UNION ALL SELECT from_user_id,0,0,amount_paise FROM public.settlements WHERE group_id=$1 AND status='confirmed'
 UNION ALL SELECT to_user_id,0,0,-amount_paise FROM public.settlements WHERE group_id=$1 AND status='confirmed'
 ), totals AS (SELECT user_id,sum(paid)::bigint paid,sum(owed)::bigint owed,sum(delta)::bigint balance FROM entries GROUP BY user_id)
 SELECT u.id::text,u.name,m.role,m.status,COALESCE(t.paid,0),COALESCE(t.owed,0),COALESCE(t.balance,0)
 FROM public.group_members m JOIN public.users u ON u.id=m.user_id LEFT JOIN totals t ON t.user_id=u.id WHERE m.group_id=$1 ORDER BY u.name,u.id`, gid)
	if err != nil {
		return err
	}
	members := []map[string]any{}
	balances := map[string]int64{}
	var total int64
	for rows.Next() {
		var id, n, ro, status string
		var paid, owed, balance int64
		if err = rows.Scan(&id, &n, &ro, &status, &paid, &owed, &balance); err != nil {
			rows.Close()
			return err
		}
		total += paid
		balances[id] = balance
		members = append(members, map[string]any{"id": id, "name": n, "role": ro, "status": status, "paid_paise": paid, "share_paise": owed, "balance_paise": balance})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var expenses, settlements []byte
	err = tx.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(x)),'[]'::jsonb) FROM (
 SELECT e.id,e.description,e.amount_paise,e.paid_by,e.created_by,e.version,e.created_at,u.name AS payer_name,
 (SELECT jsonb_agg(s.user_id ORDER BY s.user_id) FROM public.expense_splits s WHERE s.expense_id=e.id) AS participant_ids
 FROM public.expenses e JOIN public.users u ON u.id=e.paid_by WHERE e.group_id=$1 AND e.voided_at IS NULL ORDER BY e.created_at DESC,e.id DESC LIMIT 100) x`, gid).Scan(&expenses)
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(x)),'[]'::jsonb) FROM (
 SELECT s.id,s.from_user_id,s.to_user_id,s.amount_paise,s.status,s.created_at,f.name AS from_name,t.name AS to_name
 FROM public.settlements s JOIN public.users f ON f.id=s.from_user_id JOIN public.users t ON t.id=s.to_user_id WHERE s.group_id=$1 ORDER BY (s.status='pending') DESC,s.created_at DESC LIMIT 100) x`, gid).Scan(&settlements)
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	dbDuration += time.Since(dbPhaseStart)
	payload := map[string]any{"id": gid, "name": name, "financial_version": version, "total_paise": total, "members": members, "expenses": json.RawMessage(expenses), "settlements": json.RawMessage(settlements), "suggestions": ledger.Suggest(balances)}
	if a.CacheEnabled && a.Cache != nil {
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		cached, err := encodeCachedDashboard(version, payloadJSON)
		if err != nil {
			return err
		}
		ttl := a.CacheTTL
		if ttl <= 0 {
			ttl = 60_000 * time.Millisecond
		}
		cacheSetStart := time.Now()
		if err = a.Cache.Set(ctx, dashboardCacheKey(gid, version), cached, ttl); err != nil {
			cacheSetDuration = time.Since(cacheSetStart)
			slog.Info("dashboard cache error", "group_id", gid)
			w.Header().Set("X-Cache", "ERROR")
		} else {
			cacheSetDuration = time.Since(cacheSetStart)
		}
	}
	payload["role"] = role
	setTiming()
	send(w, 200, payload)
	return nil
}
