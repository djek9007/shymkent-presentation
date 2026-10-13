package app

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

type ManagedUser struct {
	ID                 int64             `json:"id"`
	Org                int64             `json:"organization_id"`
	Login              string            `json:"login"`
	Active             bool              `json:"active"`
	MustChange         bool              `json:"must_change"`
	OrganizationName   map[string]string `json:"organization_name"`
	OrganizationActive bool              `json:"organization_active"`
}

func (a *App) users(w http.ResponseWriter, r *http.Request) {
	if !a.requireCentral(w, r) {
		return
	}
	p, offset := page(r)
	q := r.URL.Query().Get("q")
	where := ` FROM users u LEFT JOIN organizations o ON o.id=u.organization_id WHERE ($1='' OR u.login ILIKE '%'||$1||'%' OR o.name::text ILIKE '%'||$1||'%')`
	var total int
	if err := a.DB.QueryRow(r.Context(), "SELECT count(*)"+where, q).Scan(&total); err != nil {
		a.dbError(w, err)
		return
	}
	rows, err := a.DB.Query(r.Context(), `SELECT u.id,COALESCE(u.organization_id,0),u.login,u.active,u.must_change,COALESCE(o.name,'{}'::jsonb),COALESCE(o.active,true)`+where+` ORDER BY u.id LIMIT 20 OFFSET $2`, q, offset)
	if err != nil {
		a.dbError(w, err)
		return
	}
	defer rows.Close()
	items := []ManagedUser{}
	for rows.Next() {
		var u ManagedUser
		if err = rows.Scan(&u.ID, &u.Org, &u.Login, &u.Active, &u.MustChange, &u.OrganizationName, &u.OrganizationActive); err != nil {
			a.dbError(w, err)
			return
		}
		items = append(items, u)
	}
	if err = rows.Err(); err != nil {
		a.dbError(w, err)
		return
	}
	write(w, 200, map[string]any{"items": items, "total": total, "page": p, "page_size": 20})
}
func validLogin(login string) bool {
	return len(login) >= 3 && len(login) <= 100 && strings.TrimSpace(login) == login && !strings.ContainsAny(login, " \t\n\r")
}
func (a *App) userError(w http.ResponseWriter, err error) {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		fields(w, map[string]string{"login": "Логин занят"})
		return
	}
	a.dbError(w, err)
}
func (a *App) createUser(w http.ResponseWriter, r *http.Request) {
	if !a.requireCentral(w, r) {
		return
	}
	var in struct {
		Org   int64  `json:"organization_id"`
		Login string `json:"login"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !validLogin(in.Login) || in.Org <= 0 {
		fields(w, map[string]string{"account": "Выберите организацию и логин (3–100 байт, без пробелов)"})
		return
	}
	password := token()[:24]
	h, err := passwordHash(password)
	if err != nil {
		a.dbError(w, err)
		return
	}
	tx, err := a.DB.Begin(r.Context())
	if err != nil {
		a.dbError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var active bool
	if err = tx.QueryRow(r.Context(), "SELECT active FROM organizations WHERE id=$1 FOR UPDATE", in.Org).Scan(&active); err != nil {
		a.dbError(w, err)
		return
	}
	if !active {
		fields(w, map[string]string{"organization_id": "Организация отключена"})
		return
	}
	if _, err = tx.Exec(r.Context(), "INSERT INTO users(organization_id,login,password_hash) VALUES($1,$2,$3)", in.Org, in.Login, h); err != nil {
		a.userError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		a.dbError(w, err)
		return
	}
	write(w, 201, map[string]string{"login": in.Login, "temporary_password": password})
}
func (a *App) updateUser(w http.ResponseWriter, r *http.Request) {
	if !a.requireCentral(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(w, 404, "Не найдено")
		return
	}
	var in struct {
		Login  string `json:"login"`
		Active bool   `json:"active"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !validLogin(in.Login) {
		fields(w, map[string]string{"login": "От 3 до 100 байт, без пробелов"})
		return
	}
	tx, err := a.DB.Begin(r.Context())
	if err != nil {
		a.dbError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var org int64
	if err = tx.QueryRow(r.Context(), "SELECT COALESCE(organization_id,0) FROM users WHERE id=$1 FOR UPDATE", id).Scan(&org); err != nil {
		a.dbError(w, err)
		return
	}
	if org == 0 {
		fail(w, 403, "Учётные записи центральных администраторов защищены от изменения в панели")
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE users SET login=$1,active=$2 WHERE id=$3", in.Login, in.Active, id); err != nil {
		a.userError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "DELETE FROM sessions WHERE user_id=$1", id); err != nil {
		a.dbError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		a.dbError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (a *App) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	if !a.requireCentral(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(w, 404, "Не найдено")
		return
	}
	password := token()[:24]
	h, err := passwordHash(password)
	if err != nil {
		a.dbError(w, err)
		return
	}
	tx, err := a.DB.Begin(r.Context())
	if err != nil {
		a.dbError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	var org int64
	var login string
	if err = tx.QueryRow(r.Context(), "SELECT COALESCE(organization_id,0),login FROM users WHERE id=$1 FOR UPDATE", id).Scan(&org, &login); err != nil {
		a.dbError(w, err)
		return
	}
	if org == 0 {
		fail(w, 403, "Учётные записи центральных администраторов защищены от изменения в панели")
		return
	}
	if _, err = tx.Exec(r.Context(), "UPDATE users SET password_hash=$1,must_change=true WHERE id=$2", h, id); err != nil {
		a.dbError(w, err)
		return
	}
	if _, err = tx.Exec(r.Context(), "DELETE FROM sessions WHERE user_id=$1", id); err != nil {
		a.dbError(w, err)
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		a.dbError(w, err)
		return
	}
	write(w, 200, map[string]string{"login": login, "temporary_password": password})
}
