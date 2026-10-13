package app

import (
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func scanOrg(row pgx.Row) (Organization, error) {
	o := Organization{}
	e := row.Scan(&o.ID, &o.Slug, &o.Kind, &o.Active, &o.Name, &o.Profile, &o.Hidden)
	return o, e
}

const orgColumns = "id,slug,kind,active,name,profile,hidden_sections"

func (a *App) site(w http.ResponseWriter, r *http.Request) {
	st := state(r)
	menu, err := a.readMenu(r, st.Org)
	if err != nil {
		a.dbError(w, err)
		return
	}
	if st.Central {
		write(w, 200, map[string]any{"central": true, "domain": a.Domain, "menu": menu})
		return
	}
	o, e := scanOrg(a.DB.QueryRow(r.Context(), "SELECT "+orgColumns+" FROM organizations WHERE id=$1", st.Org))
	if e != nil {
		a.dbError(w, e)
		return
	}
	write(w, 200, map[string]any{"central": false, "domain": a.Domain, "organization": o, "menu": menu})
}
func (a *App) catalog(w http.ResponseWriter, r *http.Request) {
	if !state(r).Central {
		fail(w, 404, "Не найдено")
		return
	}
	a.listOrgs(w, r, true)
}
func (a *App) organizations(w http.ResponseWriter, r *http.Request) { a.listOrgs(w, r, false) }
func (a *App) listOrgs(w http.ResponseWriter, r *http.Request, public bool) {
	p, offset := page(r)
	q := r.URL.Query().Get("q")
	kind := r.URL.Query().Get("kind")
	var own int64
	if !public {
		own = state(r).User.Org
	}
	where := ` WHERE ($1=false OR active) AND ($2=0 OR id=$2) AND ($3='' OR name::text ILIKE '%'||$3||'%' OR slug ILIKE '%'||$3||'%') AND ($4='' OR kind=$4)`
	var count int
	e := a.DB.QueryRow(r.Context(), "SELECT count(*) FROM organizations"+where, public, own, q, kind).Scan(&count)
	if e != nil {
		a.dbError(w, e)
		return
	}
	rows, e := a.DB.Query(r.Context(), "SELECT "+orgColumns+" FROM organizations"+where+" ORDER BY id LIMIT 20 OFFSET $5", public, own, q, kind, offset)
	if e != nil {
		a.dbError(w, e)
		return
	}
	defer rows.Close()
	items := []Organization{}
	for rows.Next() {
		o, e := scanOrg(rows)
		if e != nil {
			a.dbError(w, e)
			return
		}
		items = append(items, o)
	}
	if e = rows.Err(); e != nil {
		a.dbError(w, e)
		return
	}
	write(w, 200, map[string]any{"items": items, "total": count, "page": p, "page_size": 20})
}
func orgErrors(o Organization) map[string]string {
	m := map[string]string{}
	if !slugRE.MatchString(o.Slug) || o.Slug == "www" || o.Slug == "admin" || o.Slug == "api" {
		m["slug"] = "Латинские буквы, цифры и дефисы; 1–63 символа; www/admin/api зарезервированы"
	}
	if o.Kind != "school" && o.Kind != "preschool" {
		m["kind"] = "Выберите тип организации"
	}
	if strings.TrimSpace(o.Name["kk"]) == "" && strings.TrimSpace(o.Name["ru"]) == "" {
		m["name"] = "Добавьте название хотя бы на одном языке"
	}
	for k, v := range o.Name {
		if (k != "kk" && k != "ru") || len(v) > 500 {
			m["name"] = "Только kk/ru, до 500 байт"
		}
	}
	for _, k := range o.Hidden {
		if !kinds[k] {
			m["hidden_sections"] = "Неизвестный раздел"
		}
	}
	for k, v := range o.Profile {
		if len(v) > 10000 {
			m["profile"] = "Поле слишком длинное"
		}
		switch k {
		case "address_kk", "address_ru", "phone", "email", "summary_kk", "summary_ru", "map_url", "logo":
		default:
			m["profile"] = "Неизвестное поле"
		}
		if k == "map_url" && v != "" && !strings.HasPrefix(v, "https://") {
			m["map_url"] = "Ссылка должна начинаться с https://"
		}
	}
	return m
}
func (a *App) saveOrganization(w http.ResponseWriter, r *http.Request) {
	var in Organization
	if !decode(w, r, &in) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	create := r.Method == "POST"
	if create {
		if !a.requireCentral(w, r) {
			return
		}
	} else {
		if id <= 0 {
			fail(w, 404, "Не найдено")
			return
		}
		if !a.allow(w, r, id) {
			return
		}
	}
	if m := orgErrors(in); len(m) > 0 {
		fields(w, m)
		return
	}
	if in.Profile == nil {
		in.Profile = map[string]string{}
	}
	if in.Hidden == nil {
		in.Hidden = []string{}
	}
	tx, e := a.DB.Begin(r.Context())
	if e != nil {
		a.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	if create {
		e = tx.QueryRow(r.Context(), "INSERT INTO organizations(slug,kind,active,name,profile,hidden_sections) VALUES($1,$2,$3,$4,$5,$6) RETURNING id", in.Slug, in.Kind, in.Active, in.Name, in.Profile, in.Hidden).Scan(&id)
	} else {
		old, err := scanOrg(tx.QueryRow(r.Context(), "SELECT "+orgColumns+" FROM organizations WHERE id=$1 FOR UPDATE", id))
		if err != nil {
			a.dbError(w, err)
			return
		}
		if in.Slug != old.Slug {
			fields(w, map[string]string{"slug": "Идентификатор нельзя изменить"})
			return
		}
		if state(r).User.Org != 0 {
			in.Active = old.Active
			in.Kind = old.Kind
		}
		if !in.Active {
			if _, e = tx.Exec(r.Context(), "SELECT id FROM users WHERE organization_id=$1 FOR UPDATE", id); e != nil {
				a.dbError(w, e)
				return
			}
			if _, e = tx.Exec(r.Context(), "DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE organization_id=$1)", id); e != nil {
				a.dbError(w, e)
				return
			}
		}
		_, e = tx.Exec(r.Context(), "UPDATE organizations SET kind=$1,active=$2,name=$3,profile=$4,hidden_sections=$5 WHERE id=$6", in.Kind, in.Active, in.Name, in.Profile, in.Hidden, id)
	}
	if e != nil {
		var pe *pgconn.PgError
		if errors.As(e, &pe) && pe.Code == "23505" {
			fields(w, map[string]string{"slug": "Идентификатор уже занят"})
		} else {
			a.dbError(w, e)
		}
		return
	}
	if in.Profile["logo"] != "" {
		var ok bool
		e = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM uploads WHERE id=$1 AND organization_id=$2 AND purpose='logo')", in.Profile["logo"], id).Scan(&ok)
		if e != nil {
			a.dbError(w, e)
			return
		}
		if !ok {
			fields(w, map[string]string{"logo": "Логотип не принадлежит организации"})
			return
		}
	}
	if e = tx.Commit(r.Context()); e != nil {
		a.dbError(w, e)
		return
	}
	in.ID = id
	write(w, 200, in)
}
func (a *App) account(w http.ResponseWriter, r *http.Request) {
	if !a.requireCentral(w, r) {
		return
	}
	id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if e != nil || id <= 0 {
		fail(w, 404, "Не найдено")
		return
	}
	var in struct {
		Login string `json:"login"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Login) < 3 || len(in.Login) > 100 {
		fields(w, map[string]string{"login": "От 3 до 100 символов"})
		return
	}
	password := token()[:24]
	h, e := passwordHash(password)
	if e != nil {
		a.dbError(w, e)
		return
	}
	tx, e := a.DB.Begin(r.Context())
	if e != nil {
		a.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	var exists int64
	if e = tx.QueryRow(r.Context(), "SELECT id FROM organizations WHERE id=$1 FOR UPDATE", id).Scan(&exists); e != nil {
		a.dbError(w, e)
		return
	}
	var user int64
	e = tx.QueryRow(r.Context(), "SELECT id FROM users WHERE organization_id=$1 ORDER BY id LIMIT 1 FOR UPDATE", id).Scan(&user)
	if errors.Is(e, pgx.ErrNoRows) {
		e = tx.QueryRow(r.Context(), "INSERT INTO users(organization_id,login,password_hash) VALUES($1,$2,$3) RETURNING id", id, in.Login, h).Scan(&user)
	} else if e == nil {
		_, e = tx.Exec(r.Context(), "UPDATE users SET login=$1,password_hash=$2,must_change=true WHERE id=$3", in.Login, h, user)
	}
	if e != nil {
		var pe *pgconn.PgError
		if errors.As(e, &pe) && pe.Code == "23505" {
			fields(w, map[string]string{"login": "Логин занят"})
		} else {
			a.dbError(w, e)
		}
		return
	}
	if _, e = tx.Exec(r.Context(), "DELETE FROM sessions WHERE user_id=$1", user); e != nil {
		a.dbError(w, e)
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		a.dbError(w, e)
		return
	}
	write(w, 200, map[string]string{"login": in.Login, "temporary_password": password})
}
func scanContent(row pgx.Row) (Content, error) {
	c := Content{Files: []Upload{}}
	e := row.Scan(&c.ID, &c.Org, &c.Kind, &c.Published, &c.Position, &c.Translations, &c.Updated, &c.ScheduleDate)
	return c, e
}

const contentColumns = "id,COALESCE(organization_id,0),kind,published,position,translations,updated_at,COALESCE(schedule_date::text,'')"

func (a *App) publicContent(w http.ResponseWriter, r *http.Request) { a.listContent(w, r, true) }
func (a *App) adminContent(w http.ResponseWriter, r *http.Request)  { a.listContent(w, r, false) }
func (a *App) listContent(w http.ResponseWriter, r *http.Request, public bool) {
	org := state(r).Org
	if !public {
		var ok bool
		org, ok = requestOrg(w, r)
		if !ok {
			return
		}
	}
	kind := r.URL.Query().Get("kind")
	p, offset := page(r)
	where := ` WHERE COALESCE(organization_id,0)=$1 AND ($2='' OR kind=$2) AND ($3=false OR published)`
	var total int
	e := a.DB.QueryRow(r.Context(), "SELECT count(*) FROM content"+where, org, kind, public).Scan(&total)
	if e != nil {
		a.dbError(w, e)
		return
	}
	rows, e := a.DB.Query(r.Context(), "SELECT "+contentColumns+" FROM content"+where+" ORDER BY position,updated_at DESC,id DESC LIMIT 20 OFFSET $4", org, kind, public, offset)
	if e != nil {
		a.dbError(w, e)
		return
	}
	items := []Content{}
	for rows.Next() {
		c, e := scanContent(rows)
		if e != nil {
			rows.Close()
			a.dbError(w, e)
			return
		}
		items = append(items, c)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		a.dbError(w, e)
		return
	}
	for i := range items {
		if e = a.attachFiles(r, &items[i]); e != nil {
			a.dbError(w, e)
			return
		}
	}
	write(w, 200, map[string]any{"items": items, "total": total, "page": p, "page_size": 20})
}
func (a *App) publicDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(w, 404, "Не найдено")
		return
	}
	c, e := scanContent(a.DB.QueryRow(r.Context(), "SELECT "+contentColumns+" FROM content WHERE id=$1 AND COALESCE(organization_id,0)=$2 AND published", r.PathValue("id"), state(r).Org))
	if e != nil {
		a.dbError(w, e)
		return
	}
	if e = a.attachFiles(r, &c); e != nil {
		a.dbError(w, e)
		return
	}
	write(w, 200, c)
}
func (a *App) saveContent(w http.ResponseWriter, r *http.Request) {
	org, ok := requestOrg(w, r)
	if !ok {
		return
	}
	var in Content
	if !decode(w, r, &in) {
		return
	}
	m := map[string]string{}
	if in.Kind == "schedule" {
		if _, err := time.Parse("2006-01-02", in.ScheduleDate); err != nil {
			m["schedule_date"] = "Укажите дату обновления"
		}
	} else {
		in.ScheduleDate = ""
	}
	if !kinds[in.Kind] {
		m["kind"] = "Неизвестный раздел"
	}
	if org == 0 && in.Kind != "news" && in.Kind != "documents" {
		m["kind"] = "Портал: только новости и документы"
	}
	has := false
	for lang, t := range in.Translations {
		if lang != "kk" && lang != "ru" {
			m["translations"] = "Разрешены kk и ru"
		}
		if len(t.Title) > 500 || len(t.Body) > 500000 {
			m["translations"] = "Слишком длинный материал"
		}
		if strings.TrimSpace(t.Title) != "" {
			has = true
		}
		t.Body = a.Policy.Sanitize(t.Body)
		in.Translations[lang] = t
	}
	if !has {
		m["translations"] = "Добавьте заголовок хотя бы на одном языке"
	}
	if len(m) > 0 {
		fields(w, m)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var e error
	if r.Method == "POST" {
		e = a.DB.QueryRow(r.Context(), "INSERT INTO content(organization_id,kind,published,position,translations,schedule_date) VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::date) RETURNING id", nullable(org), in.Kind, in.Published, in.Position, in.Translations, in.ScheduleDate).Scan(&id)
	} else {
		var existing int64
		var oldKind string
		e = a.DB.QueryRow(r.Context(), "SELECT COALESCE(organization_id,0),kind FROM content WHERE id=$1", id).Scan(&existing, &oldKind)
		if e != nil {
			a.dbError(w, e)
			return
		}
		if !a.allow(w, r, existing) {
			return
		}
		if existing != org {
			fail(w, 403, "Неверная организация")
			return
		}
		if oldKind != in.Kind {
			fields(w, map[string]string{"kind": "Раздел нельзя изменить"})
			return
		}
		_, e = a.DB.Exec(r.Context(), "UPDATE content SET published=$1,position=$2,translations=$3,updated_at=now(),schedule_date=NULLIF($6,'')::date WHERE id=$4 AND COALESCE(organization_id,0)=$5", in.Published, in.Position, in.Translations, id, org, in.ScheduleDate)
	}
	if e != nil {
		var pe *pgconn.PgError
		if errors.As(e, &pe) && pe.Code == "23505" {
			fields(w, map[string]string{"kind": "В этом разделе уже есть запись; отредактируйте её"})
		} else {
			a.dbError(w, e)
		}
		return
	}
	c, e := scanContent(a.DB.QueryRow(r.Context(), "SELECT "+contentColumns+" FROM content WHERE id=$1", id))
	if e != nil {
		a.dbError(w, e)
		return
	}
	if e = a.attachFiles(r, &c); e != nil {
		a.dbError(w, e)
		return
	}
	write(w, 200, c)
}
func (a *App) deleteContent(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(w, 404, "Не найдено")
		return
	}
	var org int64
	e := a.DB.QueryRow(r.Context(), "SELECT COALESCE(organization_id,0) FROM content WHERE id=$1", r.PathValue("id")).Scan(&org)
	if e != nil {
		a.dbError(w, e)
		return
	}
	if !a.allow(w, r, org) {
		return
	}
	tx, e := a.DB.Begin(r.Context())
	if e != nil {
		a.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	if _, e = tx.Exec(r.Context(), "DELETE FROM uploads WHERE content_id=$1", r.PathValue("id")); e != nil {
		a.dbError(w, e)
		return
	}
	if _, e = tx.Exec(r.Context(), "DELETE FROM content WHERE id=$1", r.PathValue("id")); e != nil {
		a.dbError(w, e)
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		a.dbError(w, e)
		return
	}
	w.WriteHeader(204)
}

func (a *App) preview(w http.ResponseWriter, r *http.Request) {
	if _, ok := requestOrg(w, r); !ok {
		return
	}
	var c Content
	if !decode(w, r, &c) {
		return
	}
	for lang, v := range c.Translations {
		if lang != "kk" && lang != "ru" {
			fields(w, map[string]string{"translations": "Только kk/ru"})
			return
		}
		v.Body = a.Policy.Sanitize(v.Body)
		c.Translations[lang] = v
	}
	write(w, 200, c)
}
