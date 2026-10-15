package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/crypto/bcrypt"
)

type App struct {
	DB                    *pgxpool.Pool
	Domain, Addr, Uploads string
	Secure                bool
	TrustProxy            bool
	Policy                *bluemonday.Policy
}
type User struct {
	ID         int64  `json:"id"`
	Org        int64  `json:"organization_id"`
	Login      string `json:"login"`
	MustChange bool   `json:"must_change"`
	CSRF       string `json:"csrf"`
}
type Organization struct {
	ID      int64             `json:"id"`
	Slug    string            `json:"slug"`
	Kind    string            `json:"kind"`
	Active  bool              `json:"active"`
	Name    map[string]string `json:"name"`
	Profile map[string]string `json:"profile"`
	Hidden  []string          `json:"hidden_sections"`
}
type Translation struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}
type Content struct {
	ScheduleDate string                 `json:"schedule_date"`
	ID           int64                  `json:"id"`
	Org          int64                  `json:"organization_id"`
	Kind         string                 `json:"kind"`
	Published    bool                   `json:"published"`
	Position     int                    `json:"position"`
	Translations map[string]Translation `json:"translations"`
	Updated      time.Time              `json:"updated_at"`
	Files        []Upload               `json:"files"`
}
type requestState struct {
	Org     int64
	Central bool
	User    *User
}
type stateKey struct{}

var kinds = map[string]bool{"about": true, "administration": true, "teachers": true, "news": true, "documents": true, "gallery": true, "banners": true, "schedule": true}
var slugRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func New() (*App, error) {
	d := os.Getenv("BASE_DOMAIN")
	if d == "" {
		d = "school.localhost"
	}
	pool, e := pgxpool.New(context.Background(), os.Getenv("DATABASE_URL"))
	if e != nil {
		return nil, e
	}
	if e = pool.Ping(context.Background()); e != nil {
		pool.Close()
		return nil, e
	}
	uploads := os.Getenv("UPLOAD_DIR")
	if uploads == "" {
		uploads = "var/uploads"
	}
	if e = os.MkdirAll(uploads, 0700); e != nil {
		return nil, e
	}
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	p := bluemonday.NewPolicy()
	p.AllowElements("p", "br", "h2", "h3", "strong", "em", "u", "ol", "ul", "li", "blockquote")
	p.AllowAttrs("href").OnElements("a")
	p.AllowURLSchemes("https", "http", "mailto")
	p.RequireParseableURLs(true)
	p.RequireNoFollowOnLinks(true)
	return &App{DB: pool, Domain: d, Addr: addr, Uploads: uploads, Secure: os.Getenv("DEV_HTTP") != "1", TrustProxy: os.Getenv("TRUST_PROXY") == "1", Policy: p}, nil
}
func (a *App) Command(args []string) error {
	switch args[0] {
	case "seed-demo":
		if err := a.seedDemo(); err != nil {
			return err
		}
		if err := a.seedNews(); err != nil {
			return err
		}
		return a.seedSchool()
	case "seed-school":
		return a.seedSchool()
	case "seed-news":
		return a.seedNews()
	case "migrate":
		b, e := os.ReadFile("migrations/001.sql")
		if e != nil {
			return e
		}
		_, e = a.DB.Exec(context.Background(), string(b))
		return e
	case "create-admin":
		if len(args) != 2 {
			return errors.New("usage: create-admin LOGIN; password from ADMIN_PASSWORD environment")
		}
		pw := os.Getenv("ADMIN_PASSWORD")
		if !validPassword(pw) {
			return errors.New("ADMIN_PASSWORD: 12–72 bytes required")
		}
		h, e := bcrypt.GenerateFromPassword([]byte(pw), 12)
		if e != nil {
			return e
		}
		_, e = a.DB.Exec(context.Background(), "INSERT INTO users(login,password_hash,must_change) VALUES($1,$2,false)", args[1], string(h))
		return e
	}
	return errors.New("unknown command")
}
func token() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func hash(s string) string        { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func validPassword(s string) bool { return len(s) >= 12 && len(s) <= 72 }
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, s string) { write(w, status, map[string]any{"error": s}) }
func fields(w http.ResponseWriter, m map[string]string) {
	write(w, 422, map[string]any{"error": "Проверьте поля формы", "fields": m})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		fail(w, 422, "Некорректный JSON")
		return false
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		fail(w, 422, "Лишние данные")
		return false
	}
	return true
}
func state(r *http.Request) *requestState { return r.Context().Value(stateKey{}).(*requestState) }
func (a *App) dbError(w http.ResponseWriter, e error) {
	if errors.Is(e, pgx.ErrNoRows) {
		fail(w, 404, "Не найдено")
	} else {
		log.Printf("database: %v", e)
		fail(w, 500, "Ошибка сервера")
	}
}
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/site", a.site)
	mux.HandleFunc("GET /api/v1/catalog", a.catalog)
	mux.HandleFunc("GET /api/v1/content", a.publicContent)
	mux.HandleFunc("GET /api/v1/content/{id}", a.publicDetail)
	mux.HandleFunc("POST /api/v1/auth/login", a.login)
	mux.HandleFunc("GET /api/v1/auth/me", a.me)
	mux.HandleFunc("POST /api/v1/auth/logout", a.logout)
	mux.HandleFunc("POST /api/v1/auth/password", a.password)
	mux.HandleFunc("GET /api/v1/admin/organizations", a.organizations)
	mux.HandleFunc("POST /api/v1/admin/organizations", a.saveOrganization)
	mux.HandleFunc("PUT /api/v1/admin/organizations/{id}", a.saveOrganization)
	mux.HandleFunc("POST /api/v1/admin/organizations/{id}/account", a.account)
	mux.HandleFunc("GET /api/v1/admin/menu", a.menu)
	mux.HandleFunc("PUT /api/v1/admin/menu", a.menu)
	mux.HandleFunc("GET /api/v1/admin/users", a.users)
	mux.HandleFunc("POST /api/v1/admin/users", a.createUser)
	mux.HandleFunc("PUT /api/v1/admin/users/{id}", a.updateUser)
	mux.HandleFunc("POST /api/v1/admin/users/{id}/reset-password", a.resetUserPassword)
	mux.HandleFunc("GET /api/v1/admin/content", a.adminContent)
	mux.HandleFunc("POST /api/v1/admin/preview", a.preview)
	mux.HandleFunc("POST /api/v1/admin/content", a.saveContent)
	mux.HandleFunc("PUT /api/v1/admin/content/{id}", a.saveContent)
	mux.HandleFunc("DELETE /api/v1/admin/content/{id}", a.deleteContent)
	mux.HandleFunc("POST /api/v1/admin/uploads", a.upload)
	mux.HandleFunc("PUT /api/v1/admin/uploads/{id}", a.editUpload)
	mux.HandleFunc("DELETE /api/v1/admin/uploads/{id}", a.deleteUpload)
	mux.HandleFunc("GET /files/{id}", a.file)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if e := a.DB.Ping(ctx); e != nil {
			fail(w, 503, "Database unavailable")
			return
		}
		write(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/v1/host-check", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fail(w, 404, "Не найдено") })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if e := recover(); e != nil {
				log.Printf("panic: %v", e)
				fail(w, 500, "Ошибка сервера")
			}
		}()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		host := strings.ToLower(r.Host)
		if h, _, e := net.SplitHostPort(host); e == nil {
			host = h
		}
		hostStatus := 404
		if r.URL.Path == "/api/v1/host-check" {
			hostStatus = 403
		}
		st := &requestState{Central: host == a.Domain}
		if !st.Central {
			suffix := "." + a.Domain
			if !strings.HasSuffix(host, suffix) {
				fail(w, hostStatus, "Неизвестный сайт")
				return
			}
			slug := strings.TrimSuffix(host, suffix)
			if !slugRE.MatchString(slug) {
				fail(w, hostStatus, "Неизвестный сайт")
				return
			}
			if e := a.DB.QueryRow(r.Context(), "SELECT id FROM organizations WHERE slug=$1 AND active", slug).Scan(&st.Org); e != nil {
				if errors.Is(e, pgx.ErrNoRows) {
					fail(w, hostStatus, "Сайт недоступен")
				} else {
					a.dbError(w, e)
				}
				return
			}
		}
		r = r.WithContext(context.WithValue(r.Context(), stateKey{}, st))
		admin := strings.HasPrefix(r.URL.Path, "/api/v1/admin/")
		auth := strings.HasPrefix(r.URL.Path, "/api/v1/auth/")

		if admin || auth || strings.HasPrefix(r.URL.Path, "/files/") {
			if c, e := r.Cookie("session"); e == nil {
				u := &User{}
				e = a.DB.QueryRow(r.Context(), `SELECT u.id,COALESCE(u.organization_id,0),u.login,u.must_change,s.csrf FROM sessions s JOIN users u ON u.id=s.user_id LEFT JOIN organizations o ON o.id=u.organization_id WHERE s.token_hash=$1 AND s.expires_at>now() AND (u.organization_id IS NULL OR o.active) AND u.active AND COALESCE(u.organization_id,0)=$2`, hash(c.Value), st.Org).Scan(&u.ID, &u.Org, &u.Login, &u.MustChange, &u.CSRF)
				if e == nil {
					st.User = u
				} else if !errors.Is(e, pgx.ErrNoRows) {
					a.dbError(w, e)
					return
				}
			}
		}
		if admin || (auth && r.URL.Path != "/api/v1/auth/login") {
			if st.User == nil {
				fail(w, 401, "Войдите в систему")
				return
			}
			if st.User.MustChange && r.URL.Path != "/api/v1/auth/password" && r.URL.Path != "/api/v1/auth/logout" && r.URL.Path != "/api/v1/auth/me" {
				fail(w, 403, "Смените временный пароль")
				return
			}
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			expected := "https://" + r.Host
			if !a.Secure {
				expected = "http://" + r.Host
			}
			if r.Header.Get("Origin") != expected {
				fail(w, 403, "Неверный Origin")
				return
			}
			if r.URL.Path != "/api/v1/auth/login" && (st.User == nil || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(st.User.CSRF)) != 1) {
				fail(w, 403, "Неверный CSRF-токен")
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
func page(r *http.Request) (int, int) {
	p, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if p < 1 {
		p = 1
	}
	if p > 100000 {
		p = 100000
	}
	return p, (p - 1) * 20
}
func requestOrg(w http.ResponseWriter, r *http.Request) (int64, bool) {
	u := state(r).User
	if u.Org != 0 {
		if raw := r.URL.Query().Get("organization_id"); raw != "" && raw != strconv.FormatInt(u.Org, 10) {
			fail(w, 403, "Нет доступа к организации")
			return 0, false
		}
		return u.Org, true
	}
	raw := r.URL.Query().Get("organization_id")
	if raw == "" {
		return 0, true
	}
	id, e := strconv.ParseInt(raw, 10, 64)
	if e != nil || id < 0 {
		fields(w, map[string]string{"organization_id": "Некорректный ID"})
		return 0, false
	}
	return id, true
}
func nullable(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
func (a *App) allow(w http.ResponseWriter, r *http.Request, org int64) bool {
	u := state(r).User
	if u == nil || u.MustChange || (u.Org != 0 && u.Org != org) {
		fail(w, 403, "Нет доступа")
		return false
	}
	return true
}
func (a *App) requireCentral(w http.ResponseWriter, r *http.Request) bool { return a.allow(w, r, 0) }

// TRUST_PROXY is only for an API reachable exclusively through a trusted reverse proxy.
func (a *App) clientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if parsed := net.ParseIP(ip); parsed != nil && (parsed.IsLoopback() || a.TrustProxy) {
		if forwarded := net.ParseIP(r.Header.Get("X-Real-IP")); forwarded != nil {
			return forwarded.String()
		}
	}
	return ip
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	key := hash(a.clientIP(r))
	var attempts int
	e := a.DB.QueryRow(r.Context(), `INSERT INTO login_attempts(key,attempts,window_start) VALUES($1,1,now()) ON CONFLICT(key) DO UPDATE SET attempts=CASE WHEN login_attempts.window_start<now()-interval '15 minutes' THEN 1 ELSE login_attempts.attempts+1 END,window_start=CASE WHEN login_attempts.window_start<now()-interval '15 minutes' THEN now() ELSE login_attempts.window_start END RETURNING attempts`, key).Scan(&attempts)
	if e != nil {
		a.dbError(w, e)
		return
	}
	if attempts > 10 {
		w.Header().Set("Retry-After", "900")
		fail(w, 429, "Слишком много попыток. Повторите через 15 минут")
		return
	}
	var id int64
	var h string
	e = a.DB.QueryRow(r.Context(), `SELECT u.id,u.password_hash FROM users u LEFT JOIN organizations o ON o.id=u.organization_id WHERE login=$1 AND (u.organization_id IS NULL OR o.active) AND u.active AND COALESCE(u.organization_id,0)=$2`, in.Login, state(r).Org).Scan(&id, &h)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		a.dbError(w, e)
		return
	}
	if h == "" {
		h = "$2a$12$R9h/cIPz0gi.URNNX3kh2OPST9/PgBkqquzi.Ss7KIUgO2t0jWMUW"
	}
	if bcrypt.CompareHashAndPassword([]byte(h), []byte(in.Password)) != nil || id == 0 {
		fail(w, 401, "Неверный логин или пароль")
		return
	}
	tx, e := a.DB.Begin(r.Context())
	if e != nil {
		a.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	if c, e := r.Cookie("session"); e == nil {
		if _, e = tx.Exec(r.Context(), "DELETE FROM sessions WHERE token_hash=$1", hash(c.Value)); e != nil {
			a.dbError(w, e)
			return
		}
	}
	// Lock the account so disabling/resetting cannot race a new session.
	var active bool
	e = tx.QueryRow(r.Context(), `SELECT (u.organization_id IS NULL OR o.active) AND u.active FROM users u LEFT JOIN organizations o ON o.id=u.organization_id WHERE u.id=$1 AND password_hash=$2 AND COALESCE(u.organization_id,0)=$3 FOR UPDATE OF u`, id, h, state(r).Org).Scan(&active)
	if e != nil || !active {
		fail(w, 401, "Учётная запись недоступна")
		return
	}
	raw, csrf := token(), token()
	_, e = tx.Exec(r.Context(), "INSERT INTO sessions(token_hash,user_id,csrf,expires_at) VALUES($1,$2,$3,now()+interval '12 hours')", hash(raw), id, csrf)
	if e != nil {
		a.dbError(w, e)
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		a.dbError(w, e)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "session", Value: raw, Path: "/", Secure: a.Secure, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 43200})
	write(w, 200, map[string]string{"csrf": csrf})
}
func (a *App) me(w http.ResponseWriter, r *http.Request) { write(w, 200, state(r).User) }
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie("session")
	if _, e := a.DB.Exec(r.Context(), "DELETE FROM sessions WHERE token_hash=$1", hash(c.Value)); e != nil {
		a.dbError(w, e)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "session", Path: "/", MaxAge: -1, Secure: a.Secure, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(204)
}
func (a *App) password(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !validPassword(in.Password) {
		fields(w, map[string]string{"password": "От 12 до 72 байт"})
		return
	}
	u := state(r).User
	tx, e := a.DB.Begin(r.Context())
	if e != nil {
		a.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	var old string
	if e = tx.QueryRow(r.Context(), "SELECT password_hash FROM users WHERE id=$1 FOR UPDATE", u.ID).Scan(&old); e != nil {
		a.dbError(w, e)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(old), []byte(in.Current)) != nil {
		fields(w, map[string]string{"current": "Неверный текущий пароль"})
		return
	}
	h, e := bcrypt.GenerateFromPassword([]byte(in.Password), 12)
	if e != nil {
		a.dbError(w, e)
		return
	}
	if _, e = tx.Exec(r.Context(), "UPDATE users SET password_hash=$1,must_change=false WHERE id=$2", string(h), u.ID); e != nil {
		a.dbError(w, e)
		return
	}
	if _, e = tx.Exec(r.Context(), "DELETE FROM sessions WHERE user_id=$1", u.ID); e != nil {
		a.dbError(w, e)
		return
	}
	raw, csrf := token(), token()
	if _, e = tx.Exec(r.Context(), "INSERT INTO sessions VALUES($1,$2,$3,now()+interval '12 hours')", hash(raw), u.ID, csrf); e != nil {
		a.dbError(w, e)
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		a.dbError(w, e)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "session", Value: raw, Path: "/", Secure: a.Secure, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 43200})
	write(w, 200, map[string]string{"csrf": csrf})
}
