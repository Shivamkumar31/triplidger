package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type App struct {
	DB           *pgxpool.Pool
	Origin       string
	Secure       bool
	limiter      *limiter
	Cache        DashboardCache
	CacheEnabled bool
	CacheTTL     time.Duration
}
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string   { return e.Message }
func bad(message string) error      { return &apiError{400, message} }
func denied() error                 { return &apiError{403, "You do not have access to this group or action"} }
func conflict(message string) error { return &apiError{409, message} }

type endpoint func(http.ResponseWriter, *http.Request) error
type userKey struct{}
type User struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func actor(r *http.Request) string { return r.Context().Value(userKey{}).(string) }
func send(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return bad("Invalid JSON body or unknown field")
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return bad("Expected a single JSON object")
	}
	return nil
}
func token() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func validID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (a *App) wrap(fn endpoint, auth bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				slog.Error("request panic")
				send(w, 500, map[string]string{"error": "Internal error"})
			}
		}()
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		if r.Method != "GET" && (r.Header.Get("Origin") != a.Origin || r.Header.Get("X-TripLedger") != "1") {
			send(w, 403, map[string]string{"error": "Invalid request origin or missing X-TripLedger header"})
			return
		}
		for _, key := range []string{"gid", "eid", "sid"} {
			if id := r.PathValue(key); id != "" && !validID(id) {
				send(w, 400, map[string]string{"error": "Invalid ID"})
				return
			}
		}
		if auth {
			c, err := r.Cookie("tripledger_session")
			var uid string
			if err != nil || a.DB.QueryRow(ctx, "SELECT user_id::text FROM public.sessions WHERE token_hash=$1 AND expires_at>now()", digest(cookieValue(c))).Scan(&uid) != nil {
				send(w, 401, map[string]string{"error": "Please sign in"})
				return
			}
			r = r.WithContext(context.WithValue(ctx, userKey{}, uid))
		}
		if err := fn(w, r); err != nil {
			status, msg := 500, "Request failed. Please try again."
			var ae *apiError
			var pe *pgconn.PgError
			if errors.As(err, &ae) {
				status, msg = ae.Status, ae.Message
			} else if errors.Is(err, pgx.ErrNoRows) {
				status, msg = 404, "Not found"
			} else if errors.As(err, &pe) {
				switch pe.Code {
				case "23505":
					status, msg = 409, "Record already exists"
				case "23503", "23514", "22P02":
					status, msg = 400, "Invalid data or relationship"
				case "40001", "40P01":
					status, msg = 409, "Concurrent change. Please retry"
				}
				slog.Error("database request failed", "code", pe.Code)
			} else {
				slog.Error("request failed", "error_type", "internal")
			}
			send(w, status, map[string]string{"error": msg})
		}
	}
}
func cookieValue(c *http.Cookie) string {
	if c == nil {
		return ""
	}
	return c.Value
}
func (a *App) Handler() http.Handler {
	a.limiter = &limiter{items: map[string]bucket{}}
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", a.wrap(func(w http.ResponseWriter, r *http.Request) error {
		if err := a.DB.Ping(r.Context()); err != nil {
			return &apiError{503, "Database unavailable"}
		}
		send(w, 200, map[string]string{"status": "ok"})
		return nil
	}, false))
	m.HandleFunc("POST /api/auth/register", a.wrap(a.register, false))
	m.HandleFunc("POST /api/auth/login", a.wrap(a.login, false))
	m.HandleFunc("POST /api/auth/logout", a.wrap(a.logout, true))
	m.HandleFunc("GET /api/me", a.wrap(a.me, true))
	m.HandleFunc("GET /api/groups", a.wrap(a.listGroups, true))
	m.HandleFunc("POST /api/groups", a.wrap(a.createGroup, true))
	m.HandleFunc("POST /api/groups/{gid}/invites", a.wrap(a.invite, true))
	m.HandleFunc("POST /api/invites/accept", a.wrap(a.accept, true))
	m.HandleFunc("GET /api/groups/{gid}/dashboard", a.wrap(a.dashboard, true))
	m.HandleFunc("POST /api/groups/{gid}/expenses", a.wrap(a.expense, true))
	m.HandleFunc("PATCH /api/groups/{gid}/expenses/{eid}", a.wrap(a.expense, true))
	m.HandleFunc("POST /api/groups/{gid}/settlements", a.wrap(a.settle, true))
	m.HandleFunc("POST /api/groups/{gid}/settlements/{sid}/action", a.wrap(a.settlementAction, true))
	return m
}
func (a *App) session(ctx context.Context, w http.ResponseWriter, uid string) error {
	raw, err := token()
	if err != nil {
		return err
	}
	_, err = a.DB.Exec(ctx, "INSERT INTO public.sessions(token_hash,user_id,expires_at) VALUES($1,$2,now()+interval '7 days')", digest(raw), uid)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "tripledger_session", Value: raw, Path: "/", HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteLaxMode, MaxAge: 604800})
	return nil
}
func credentials(w http.ResponseWriter, r *http.Request) (string, string, string, error) {
	var in struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decode(w, r, &in); err != nil {
		return "", "", "", err
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.Name = strings.TrimSpace(in.Name)
	parsed, err := mail.ParseAddress(in.Email)
	if err != nil || parsed.Address != in.Email || len(in.Email) > 254 || len(in.Password) < 10 || len(in.Password) > 72 {
		return "", "", "", bad("Use a valid email and a password of 10–72 bytes")
	}
	return in.Name, in.Email, in.Password, nil
}
func (a *App) register(w http.ResponseWriter, r *http.Request) error {
	name, email, password, err := credentials(w, r)
	if err != nil {
		return err
	}
	if !a.limiter.allow("register") {
		return &apiError{429, "Too many registrations. Try again in a minute"}
	}
	if len(name) < 1 || len(name) > 100 {
		return bad("Name must be 1–100 bytes")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	var u User
	err = a.DB.QueryRow(r.Context(), "INSERT INTO public.users(name,email,password_hash) VALUES($1,$2,$3) RETURNING id::text,name,email", name, email, string(hash)).Scan(&u.ID, &u.Name, &u.Email)
	if err != nil {
		return err
	}
	if err = a.session(r.Context(), w, u.ID); err != nil {
		return err
	}
	send(w, 201, u)
	return nil
}
func (a *App) login(w http.ResponseWriter, r *http.Request) error {
	_, email, password, err := credentials(w, r)
	if err != nil {
		return err
	}
	if !a.limiter.allow("login:"+email) || !a.limiter.allow("login-global") {
		return &apiError{429, "Too many login attempts. Try again in a minute"}
	}
	var u User
	var hash string
	err = a.DB.QueryRow(r.Context(), "SELECT id::text,name,email,password_hash FROM public.users WHERE email=$1", email).Scan(&u.ID, &u.Name, &u.Email, &hash)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return &apiError{401, "Incorrect email or password"}
	}
	if err = a.session(r.Context(), w, u.ID); err != nil {
		return err
	}
	send(w, 200, u)
	return nil
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) error {
	c, _ := r.Cookie("tripledger_session")
	if _, err := a.DB.Exec(r.Context(), "DELETE FROM public.sessions WHERE token_hash=$1", digest(cookieValue(c))); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "tripledger_session", Value: "", Path: "/", HttpOnly: true, Secure: a.Secure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	send(w, 200, map[string]bool{"ok": true})
	return nil
}
func (a *App) me(w http.ResponseWriter, r *http.Request) error {
	var u User
	err := a.DB.QueryRow(r.Context(), "SELECT id::text,name,email FROM public.users WHERE id=$1", actor(r)).Scan(&u.ID, &u.Name, &u.Email)
	if err != nil {
		return err
	}
	send(w, 200, u)
	return nil
}

type bucket struct {
	until time.Time
	n     int
}
type limiter struct {
	mu    sync.Mutex
	items map[string]bucket
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for k, v := range l.items {
		if now.After(v.until) {
			delete(l.items, k)
		}
	}
	b := l.items[key]
	if b.until.IsZero() {
		b.until = now.Add(time.Minute)
	}
	b.n++
	l.items[key] = b
	return b.n <= 20
}
