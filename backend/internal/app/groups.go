package app

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strings"
)

// All group mutations acquire this lock before reading/writing its financial state.
func (a *App) groupTx(r *http.Request) (pgx.Tx, string, error) {
	ctx := r.Context()
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		return nil, "", err
	}
	gid := r.PathValue("gid")
	var archived bool
	if err = tx.QueryRow(ctx, "SELECT archived_at IS NOT NULL FROM public.groups WHERE id=$1 FOR UPDATE", gid).Scan(&archived); err != nil {
		tx.Rollback(ctx)
		return nil, "", err
	}
	var role string
	if err = tx.QueryRow(ctx, "SELECT role FROM public.group_members WHERE group_id=$1 AND user_id=$2 AND status='active'", gid, actor(r)).Scan(&role); err != nil {
		tx.Rollback(ctx)
		return nil, "", denied()
	}
	if archived {
		tx.Rollback(ctx)
		return nil, "", conflict("This group is archived")
	}
	return tx, role, nil
}
func bump(ctx context.Context, tx pgx.Tx, gid string) error {
	_, err := tx.Exec(ctx, "UPDATE public.groups SET financial_version=financial_version+1 WHERE id=$1", gid)
	return err
}
func active(ctx context.Context, tx pgx.Tx, gid, uid string) error {
	if !validID(uid) {
		return bad("Invalid member ID")
	}
	var yes bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM public.group_members WHERE group_id=$1 AND user_id=$2 AND status='active')", gid, uid).Scan(&yes); err != nil {
		return err
	}
	if !yes {
		return bad("Payer and participants must be active group members")
	}
	return nil
}
func (a *App) listGroups(w http.ResponseWriter, r *http.Request) error {
	rows, err := a.DB.Query(r.Context(), `SELECT g.id::text,g.name,g.currency,m.role FROM public.groups g JOIN public.group_members m ON m.group_id=g.id WHERE m.user_id=$1 AND m.status='active' ORDER BY g.created_at DESC`, actor(r))
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var id, name, currency, role string
		if err = rows.Scan(&id, &name, &currency, &role); err != nil {
			return err
		}
		out = append(out, map[string]string{"id": id, "name": name, "currency": currency, "role": role})
	}
	if err = rows.Err(); err != nil {
		return err
	}
	send(w, 200, out)
	return nil
}
func (a *App) createGroup(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	in.Name = strings.TrimSpace(in.Name)
	if len(in.Name) < 1 || len(in.Name) > 100 {
		return bad("Group name must be 1–100 bytes")
	}
	ctx := r.Context()
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id string
	if err = tx.QueryRow(ctx, "INSERT INTO public.groups(name,created_by) VALUES($1,$2) RETURNING id::text", in.Name, actor(r)).Scan(&id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO public.group_members(group_id,user_id,role) VALUES($1,$2,'owner')", id, actor(r)); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	send(w, 201, map[string]string{"id": id, "name": in.Name})
	return nil
}
func (a *App) invite(w http.ResponseWriter, r *http.Request) error {
	tx, role, err := a.groupTx(r)
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	if role == "member" {
		return denied()
	}
	raw, err := token()
	if err != nil {
		return err
	}
	_, err = tx.Exec(r.Context(), "INSERT INTO public.group_invites(group_id,created_by,token_hash,expires_at) VALUES($1,$2,$3,now()+interval '7 days')", r.PathValue("gid"), actor(r), digest(raw))
	if err != nil {
		return err
	}
	if err = tx.Commit(r.Context()); err != nil {
		return err
	}
	send(w, 201, map[string]string{"url": a.Origin + "/?invite=" + raw})
	return nil
}
func (a *App) accept(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Token string `json:"token"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	if len(in.Token) != 64 {
		return bad("Invalid invitation token")
	}
	ctx := r.Context()
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var gid string
	if err = tx.QueryRow(ctx, "SELECT group_id::text FROM public.group_invites WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at>now()", digest(in.Token)).Scan(&gid); err != nil {
		return bad("Invitation expired or invalid")
	}
	var archived bool
	if err = tx.QueryRow(ctx, "SELECT archived_at IS NOT NULL FROM public.groups WHERE id=$1 FOR UPDATE", gid).Scan(&archived); err != nil {
		return err
	}
	if archived {
		return conflict("Group is archived")
	}
	var valid bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM public.group_invites WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at>now())", digest(in.Token)).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return bad("Invitation expired or invalid")
	}
	var count int
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM public.group_members WHERE group_id=$1", gid).Scan(&count); err != nil {
		return err
	}
	if count >= 100 {
		return conflict("Starter supports at most 100 members")
	}
	var status string
	err = tx.QueryRow(ctx, "SELECT status FROM public.group_members WHERE group_id=$1 AND user_id=$2", gid, actor(r)).Scan(&status)
	if err == nil && status == "left" {
		return conflict("Rejoining former members is not supported yet")
	}
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	_, err = tx.Exec(ctx, "INSERT INTO public.group_members(group_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING", gid, actor(r))
	if err != nil {
		return err
	}
	if err = bump(ctx, tx, gid); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	send(w, 200, map[string]string{"id": gid})
	return nil
}

// Called after the group lock; concurrent same-group retries serialize.
func replay(ctx context.Context, tx pgx.Tx, r *http.Request, scope string, payload any) (json.RawMessage, string, error) {
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 8 || len(key) > 100 {
		return nil, "", bad("Idempotency-Key must contain 8–100 characters")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}
	hash := digest(string(body))
	var previous string
	var response []byte
	err = tx.QueryRow(ctx, "SELECT request_hash,response FROM public.idempotency_requests WHERE actor_id=$1 AND scope=$2 AND key=$3", actor(r), scope, key).Scan(&previous, &response)
	if err == pgx.ErrNoRows {
		return nil, hash, nil
	}
	if err != nil {
		return nil, "", err
	}
	if previous != hash {
		return nil, "", conflict("Idempotency key was used for a different request")
	}
	return json.RawMessage(response), hash, nil
}
func remember(ctx context.Context, tx pgx.Tx, r *http.Request, scope, hash string, response any) error {
	b, err := json.Marshal(response)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "INSERT INTO public.idempotency_requests(actor_id,scope,key,request_hash,response) VALUES($1,$2,$3,$4,$5::jsonb)", actor(r), scope, r.Header.Get("Idempotency-Key"), hash, string(b))
	return err
}
