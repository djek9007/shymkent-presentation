package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidation(t *testing.T) {
	for _, slug := range []string{"51", "school-51-almaty", "sad-51-almaty"} {
		if !slugRE.MatchString(slug) {
			t.Fatal(slug)
		}
	}
	for _, slug := range []string{"a.b", "-51", "51-", "Школа", ""} {
		if slugRE.MatchString(slug) {
			t.Fatal(slug)
		}
	}
	if _, ok := fileType("fake.pdf", []byte("<script>alert(1)</script>")); ok {
		t.Fatal("accepted fake PDF")
	}
	if _, ok := fileType("file.pdf", []byte("%PDF-1.7\n")); !ok {
		t.Fatal("rejected PDF")
	}
	if validPassword("short") || validPassword(strings.Repeat("a", 73)) {
		t.Fatal("password limits")
	}
}
func TestIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set: use a dedicated empty database")
	}
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("BASE_DOMAIN", "school.localhost")
	t.Setenv("DEV_HTTP", "1")
	t.Setenv("UPLOAD_DIR", t.TempDir())
	a, e := New()
	if e != nil {
		t.Fatal(e)
	}
	defer a.DB.Close()
	ctx := context.Background()
	var tables int
	if e = a.DB.QueryRow(ctx, "SELECT count(*) FROM information_schema.tables WHERE table_schema='public'").Scan(&tables); e != nil || tables != 0 {
		t.Fatal("integration database must be empty", e)
	}
	sql, e := os.ReadFile("../../migrations/001.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.DB.Exec(ctx, string(sql)); e != nil {
		t.Fatal(e)
	}
	h, e := passwordHash("Initial-admin-password")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.DB.Exec(ctx, "INSERT INTO users(login,password_hash,must_change) VALUES('admin',$1,false)", h); e != nil {
		t.Fatal(e)
	}
	handler := a.Handler()
	request := func(host, method, path string, body any, cookie, csrf string) (*httptest.ResponseRecorder, map[string]any) {
		t.Helper()
		var b io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			b = bytes.NewReader(raw)
		}
		r := httptest.NewRequest(method, "http://"+host+path, b)
		r.Host = host
		if bodyMap, ok := body.(map[string]string); ok && bodyMap["login"] == "renamed-staff" {
			r.RemoteAddr = "192.0.2.50:1234"
		}
		r.Header.Set("Origin", "http://"+host)
		r.Header.Set("X-CSRF-Token", csrf)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "session", Value: cookie})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		data := map[string]any{}
		_ = json.Unmarshal(w.Body.Bytes(), &data)
		return w, data
	}
	expect := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("status %d want %d: %s", w.Code, status, w.Body.String())
		}
	}
	w, data := request("school.localhost", "POST", "/api/v1/auth/login", map[string]string{"login": "admin", "password": "Initial-admin-password"}, "", "")
	expect(w, 200)
	adminCookie := w.Result().Cookies()[0].Value
	adminCSRF := data["csrf"].(string)
	create := func(slug, kind string) int64 {
		w, d := request("school.localhost", "POST", "/api/v1/admin/organizations", Organization{Slug: slug, Kind: kind, Active: true, Name: map[string]string{"kk": "Ұйым " + slug, "ru": "Организация " + slug}}, adminCookie, adminCSRF)
		expect(w, 200)
		return int64(d["id"].(float64))
	}
	first, second := create("51", "school"), create("sad-51", "preschool")
	w, _ = request("unknown.school.localhost", "GET", "/api/v1/site", nil, "", "")
	expect(w, 404)
	w, _ = request("evil.example", "GET", "/api/v1/site", nil, "", "")
	expect(w, 404)
	w, _ = request("51.school.localhost", "GET", "/api/v1/site", nil, "", "")
	expect(w, 200)
	w, _ = request("school.localhost", "POST", "/api/v1/admin/organizations", Organization{Slug: "api", Kind: "school", Name: map[string]string{"ru": "x"}}, adminCookie, adminCSRF)
	expect(w, 422)
	account := func(id int64, login string) string {
		w, d := request("school.localhost", "POST", fmt.Sprintf("/api/v1/admin/organizations/%d/account", id), map[string]string{"login": login}, adminCookie, adminCSRF)
		expect(w, 200)
		return d["temporary_password"].(string)
	}
	temporary := account(first, "school51")
	_ = account(second, "preschool51")
	for _, attempt := range []struct{ host, login, password string }{{"school.localhost", "school51", temporary}, {"sad-51.school.localhost", "school51", temporary}, {"51.school.localhost", "admin", "Initial-admin-password"}} {
		w, _ = request(attempt.host, "POST", "/api/v1/auth/login", map[string]string{"login": attempt.login, "password": attempt.password}, "", "")
		expect(w, 401)
	}
	w, _ = request("51.school.localhost", "GET", "/api/v1/auth/me", nil, adminCookie, "")
	expect(w, 401)

	w, data = request("51.school.localhost", "POST", "/api/v1/auth/login", map[string]string{"login": "school51", "password": temporary}, "", "")
	expect(w, 200)
	if w.Result().Cookies()[0].Domain != "" {
		t.Fatal("school cookie must be host-only")
	}
	staffCookie := w.Result().Cookies()[0].Value
	staffCSRF := data["csrf"].(string)
	w, _ = request("51.school.localhost", "GET", "/api/v1/admin/content", nil, staffCookie, "")
	expect(w, 403)
	w, data = request("51.school.localhost", "POST", "/api/v1/auth/password", map[string]string{"current": temporary, "password": "Permanent-staff-password"}, staffCookie, staffCSRF)
	expect(w, 200)
	oldCookie := staffCookie
	staffCookie = w.Result().Cookies()[0].Value
	staffCSRF = data["csrf"].(string)
	if staffCookie == oldCookie {
		t.Fatal("session did not rotate")
	}
	w, _ = request("51.school.localhost", "GET", "/api/v1/auth/me", nil, oldCookie, "")
	expect(w, 401)
	w, _ = request("51.school.localhost", "GET", fmt.Sprintf("/api/v1/admin/content?organization_id=%d", second), nil, staffCookie, "")
	expect(w, 403)

	for _, host := range []string{"school.localhost", "sad-51.school.localhost"} {
		w, _ = request(host, "GET", "/api/v1/auth/me", nil, staffCookie, "")
		expect(w, 401)
		w, _ = request(host, "PUT", "/api/v1/admin/menu", defaultMenu(first), staffCookie, staffCSRF)
		expect(w, 401)
	}
	w, data = request("51.school.localhost", "GET", "/api/v1/admin/organizations", nil, staffCookie, "")
	expect(w, 200)
	if data["total"] != float64(1) {
		t.Fatal("school admin sees other organizations")
	}
	// Menu persistence, public rendering data and tenant isolation.
	if _, e = a.DB.Exec(ctx, `UPDATE organizations SET hidden_sections='["schedule"]'::jsonb WHERE id=$1`, first); e != nil {
		t.Fatal(e)
	}
	w, data = request("51.school.localhost", "GET", fmt.Sprintf("/api/v1/admin/menu?organization_id=%d", first), nil, staffCookie, "")
	expect(w, 200)
	// Menu responses are arrays; inspect the database-derived defaults directly.
	menuRequest := httptest.NewRequest("GET", "http://school.localhost", nil)
	effective, err := a.readMenu(menuRequest, first)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range effective {
		if item.Key == "schedule" && item.Visible {
			t.Fatal("legacy hidden section ignored")
		}
	}
	menu := defaultMenu(first)
	menu[1].Label["ru"] = "Наша школа"
	menu[1], menu[3] = menu[3], menu[1]
	menu = append(menu, MenuItem{Key: "link-resource", Label: map[string]string{"kk": "Ресурс", "ru": "Ресурс"}, Visible: true, URL: "https://example.kz/ru/", Parent: "group-school"})
	menuPath := fmt.Sprintf("/api/v1/admin/menu?organization_id=%d", first)
	w, _ = request("51.school.localhost", "PUT", menuPath, menu, staffCookie, staffCSRF)
	expect(w, 200)
	w, data = request("51.school.localhost", "GET", "/api/v1/site", nil, "", "")
	expect(w, 200)
	var hidden []string
	if err = a.DB.QueryRow(ctx, "SELECT hidden_sections FROM organizations WHERE id=$1", first).Scan(&hidden); err != nil || len(hidden) != 0 {
		t.Fatal("menu failed to sync visibility", err)
	}
	publicMenu := data["menu"].([]any)
	if publicMenu[3].(map[string]any)["label"].(map[string]any)["ru"] != "Наша школа" {
		t.Fatal("menu labels/order not persisted")
	}
	if publicMenu[len(publicMenu)-1].(map[string]any)["url"] != "https://example.kz/ru/" {
		t.Fatal("menu URL not persisted")
	}
	w, _ = request("51.school.localhost", "PUT", fmt.Sprintf("/api/v1/admin/menu?organization_id=%d", second), defaultMenu(second), staffCookie, staffCSRF)
	expect(w, 403)
	w, _ = request("51.school.localhost", "PUT", menuPath, menu[:2], staffCookie, staffCSRF)
	expect(w, 422)
	// Old JSON gets grouped once; intentionally flattened new JSON stays flat.
	legacyMenu := flatMenu(0)
	legacyMenu[1].Label["ru"] = "Каталог школ"
	legacyJSON, _ := json.Marshal(legacyMenu)
	if _, err = a.DB.Exec(ctx, `INSERT INTO site_settings(site_id,menu) VALUES(0,(SELECT jsonb_agg(entry-'parent'-'group') FROM jsonb_array_elements($1::jsonb) entry))`, string(legacyJSON)); err != nil {
		t.Fatal(err)
	}
	migrated, err := a.readMenu(menuRequest, 0)
	if err != nil || len(migrated) != 5 || migrated[1].Label["ru"] != "Каталог школ" {
		t.Fatal("legacy JSON migration failed", err)
	}
	w, _ = request("school.localhost", "PUT", "/api/v1/admin/menu", legacyMenu, adminCookie, adminCSRF)
	expect(w, 200)
	flattened, err := a.readMenu(menuRequest, 0)
	if err != nil || len(flattened) != 4 {
		t.Fatal("intentionally flat menu regrouped", err)
	}
	w, _ = request("51.school.localhost", "GET", "/api/v1/admin/users", nil, staffCookie, "")
	expect(w, 403)
	w, _ = request("51.school.localhost", "POST", "/api/v1/admin/users", map[string]any{"login": "other-staff", "organization_id": second}, staffCookie, staffCSRF)
	expect(w, 403)
	newAccount := map[string]any{"login": "other-staff", "organization_id": second}
	w, _ = request("school.localhost", "POST", "/api/v1/admin/users", newAccount, adminCookie, adminCSRF)
	expect(w, 201)
	w, _ = request("school.localhost", "POST", "/api/v1/admin/users", newAccount, adminCookie, adminCSRF)
	expect(w, 422)
	w, data = request("school.localhost", "GET", "/api/v1/admin/users?q=other-staff", nil, adminCookie, "")
	expect(w, 200)
	accountData := data["items"].([]any)[0].(map[string]any)
	if len(accountData) != 7 {
		t.Fatal("unexpected user fields", accountData)
	}
	accountPath := fmt.Sprintf("/api/v1/admin/users/%.0f", accountData["id"].(float64))
	w, _ = request("school.localhost", "PUT", accountPath, map[string]any{"login": "renamed-staff", "active": true}, adminCookie, adminCSRF)
	expect(w, 204)
	w, data = request("school.localhost", "POST", accountPath+"/reset-password", nil, adminCookie, adminCSRF)
	expect(w, 200)
	resetPassword := data["temporary_password"].(string)
	w, data = request("sad-51.school.localhost", "POST", "/api/v1/auth/login", map[string]string{"login": "renamed-staff", "password": resetPassword}, "", "")
	expect(w, 200)
	newCookie := w.Result().Cookies()[0].Value
	w, _ = request("school.localhost", "PUT", accountPath, map[string]any{"login": "renamed-staff", "active": false}, adminCookie, adminCSRF)
	expect(w, 204)
	w, _ = request("sad-51.school.localhost", "GET", "/api/v1/auth/me", nil, newCookie, "")
	expect(w, 401)
	w, _ = request("sad-51.school.localhost", "POST", "/api/v1/auth/login", map[string]string{"login": "renamed-staff", "password": resetPassword}, "", "")
	expect(w, 401)
	w, data = request("school.localhost", "GET", "/api/v1/auth/me", nil, adminCookie, "")
	expect(w, 200)
	w, _ = request("school.localhost", "PUT", fmt.Sprintf("/api/v1/admin/users/%.0f", data["id"].(float64)), map[string]any{"login": "admin", "active": false}, adminCookie, adminCSRF)
	expect(w, 403)
	payload := Content{Kind: "news", Translations: map[string]Translation{"ru": {Title: "Новость", Body: `<h2>Заголовок</h2><script>alert(1)</script><a href="javascript:alert(1)">link</a><img src=x onerror=alert(1)>`}}}
	w, _ = request("51.school.localhost", "POST", "/api/v1/admin/content", payload, staffCookie, "")
	expect(w, 403)
	w, data = request("51.school.localhost", "POST", "/api/v1/admin/content", payload, staffCookie, staffCSRF)
	expect(w, 200)
	cid := int64(data["id"].(float64))
	clean := data["translations"].(map[string]any)["ru"].(map[string]any)["body"].(string)
	if strings.Contains(clean, "script") || strings.Contains(clean, "onerror") {
		t.Fatal("unsafe HTML", clean)
	}
	w, _ = request("51.school.localhost", "GET", fmt.Sprintf("/api/v1/content/%d", cid), nil, "", "")
	expect(w, 404)
	var uploadID string
	{
		var b bytes.Buffer
		form := multipart.NewWriter(&b)
		_ = form.WriteField("content_id", fmt.Sprint(cid))
		f, _ := form.CreateFormFile("file", "document.pdf")
		_, _ = f.Write([]byte("%PDF-1.7\nTest\n"))
		form.Close()
		r := httptest.NewRequest("POST", "http://51.school.localhost/api/v1/admin/uploads", &b)
		r.Header.Set("Content-Type", form.FormDataContentType())
		r.Header.Set("Origin", "http://51.school.localhost")
		r.Header.Set("X-CSRF-Token", staffCSRF)
		r.AddCookie(&http.Cookie{Name: "session", Value: staffCookie})
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		expect(w, 201)
		data = map[string]any{}
		json.Unmarshal(w.Body.Bytes(), &data)
		uploadID = data["id"].(string)
	}
	w, _ = request("51.school.localhost", "GET", "/files/"+uploadID, nil, "", "")
	expect(w, 404)
	w, _ = request("51.school.localhost", "GET", "/files/"+uploadID, nil, staffCookie, "")
	expect(w, 200)
	payload.Published = true
	w, _ = request("51.school.localhost", "PUT", fmt.Sprintf("/api/v1/admin/content/%d", cid), payload, staffCookie, staffCSRF)
	expect(w, 200)
	w, data = request("51.school.localhost", "GET", fmt.Sprintf("/api/v1/content/%d", cid), nil, "", "")
	expect(w, 200)
	if _, ok := data["translations"].(map[string]any)["kk"]; ok {
		t.Fatal("invented translation")
	}
	w, _ = request("51.school.localhost", "GET", "/files/"+uploadID, nil, "", "")
	expect(w, 200)
	w, _ = request("sad-51.school.localhost", "GET", "/files/"+uploadID, nil, "", "")
	expect(w, 404)
	w, _ = request("school.localhost", "GET", "/files/"+uploadID, nil, "", "")
	expect(w, 404)
	w, _ = request("school.localhost", "PUT", fmt.Sprintf("/api/v1/admin/content/%d?organization_id=%d", cid, first), payload, "", "")
	expect(w, 401)
	// A second organization's employee cannot use direct IDs to mutate first organization's content.
	p2 := account(second, "preschool51")
	w, data = request("sad-51.school.localhost", "POST", "/api/v1/auth/login", map[string]string{"login": "preschool51", "password": p2}, "", "")
	expect(w, 200)
	c2 := w.Result().Cookies()[0].Value
	s2 := data["csrf"].(string)
	w, data = request("sad-51.school.localhost", "POST", "/api/v1/auth/password", map[string]string{"current": p2, "password": "Permanent-second-password"}, c2, s2)
	expect(w, 200)
	c2 = w.Result().Cookies()[0].Value
	s2 = data["csrf"].(string)
	w, _ = request("sad-51.school.localhost", "DELETE", fmt.Sprintf("/api/v1/admin/content/%d", cid), nil, c2, s2)
	expect(w, 403)
	w, _ = request("sad-51.school.localhost", "GET", "/files/"+uploadID, nil, c2, "")
	expect(w, 404)

	uploadTo := func(contentID int64, purpose, name string, body []byte, status int) string {
		t.Helper()
		var b bytes.Buffer
		form := multipart.NewWriter(&b)
		form.WriteField("content_id", fmt.Sprint(contentID))
		form.WriteField("purpose", purpose)
		f, _ := form.CreateFormFile("file", name)
		f.Write(body)
		form.Close()
		r := httptest.NewRequest("POST", fmt.Sprintf("http://51.school.localhost/api/v1/admin/uploads?organization_id=%d", first), &b)
		r.Header.Set("Content-Type", form.FormDataContentType())
		r.Header.Set("Origin", "http://51.school.localhost")
		r.Header.Set("X-CSRF-Token", staffCSRF)
		r.AddCookie(&http.Cookie{Name: "session", Value: staffCookie})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		expect(w, status)
		if status != 201 {
			return ""
		}
		var data map[string]any
		json.Unmarshal(w.Body.Bytes(), &data)
		return data["id"].(string)
	}
	schedule := Content{Kind: "schedule", ScheduleDate: "2026-10-11", Published: true, Translations: map[string]Translation{"ru": {Title: "Расписание"}}}
	w, data = request("51.school.localhost", "POST", "/api/v1/admin/content", schedule, staffCookie, staffCSRF)
	expect(w, 200)
	scheduleID := int64(data["id"].(float64))
	oldPDF := uploadTo(scheduleID, "content", "schedule.pdf", []byte("%PDF-1.7\nfirst"), 201)
	uploadTo(scheduleID, "content", "schedule.docx", []byte("bad"), 422)
	newPDF := uploadTo(scheduleID, "content", "schedule.pdf", []byte("%PDF-1.7\nsecond"), 201)
	w, _ = request("51.school.localhost", "GET", "/files/"+oldPDF, nil, "", "")
	expect(w, 404)
	w, _ = request("51.school.localhost", "GET", "/files/"+newPDF, nil, "", "")
	expect(w, 200)
	w, data = request("51.school.localhost", "GET", fmt.Sprintf("/api/v1/content/%d", scheduleID), nil, "", "")
	expect(w, 200)
	if len(data["files"].([]any)) != 1 {
		t.Fatal("schedule replacement must retain one PDF")
	}
	var imageData bytes.Buffer
	png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 200, 1000)))
	photoID := uploadTo(cid, "content", "photo.png", imageData.Bytes(), 201)
	w, _ = request("51.school.localhost", "GET", "/files/"+photoID+"?thumbnail=1", nil, "", "")
	expect(w, 200)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
	if err != nil || cfg.Height > 720 || cfg.Width > 720 {
		t.Fatal("thumbnail size", cfg, err)
	}
	logoID := uploadTo(scheduleID, "logo", "logo.png", imageData.Bytes(), 201)
	var logoContent *int64
	if err := a.DB.QueryRow(ctx, "SELECT content_id FROM uploads WHERE id=$1", logoID).Scan(&logoContent); err != nil || logoContent != nil {
		t.Fatal("logo must not attach to content", err)
	}
	w, _ = request("51.school.localhost", "GET", "/files/"+logoID, nil, "", "")
	expect(w, 404)
	w, data = request("51.school.localhost", "POST", "/api/v1/admin/preview", payload, staffCookie, staffCSRF)
	expect(w, 200)
	if strings.Contains(data["translations"].(map[string]any)["ru"].(map[string]any)["body"].(string), "script") {
		t.Fatal("unsafe preview")
	}
	w, data = request("51.school.localhost", "GET", fmt.Sprintf("/api/v1/content/%d", cid), nil, "", "")
	expect(w, 200)
	if data["published"] != true {
		t.Fatal("preview changed publication state")
	}
	w, _ = request("51.school.localhost", "GET", "/api/v1/content/not-a-number", nil, "", "")
	expect(w, 404)
	w, _ = request("missing.school.localhost", "GET", "/api/v1/host-check", nil, "", "")
	expect(w, 403)

	reset := account(first, "school51")
	w, _ = request("51.school.localhost", "GET", "/api/v1/auth/me", nil, staffCookie, "")
	expect(w, 401)
	w, data = request("51.school.localhost", "POST", "/api/v1/auth/login", map[string]string{"login": "school51", "password": reset}, "", "")
	expect(w, 200)
	staffCookie = w.Result().Cookies()[0].Value
	o := Organization{ID: first, Slug: "51", Kind: "school", Active: false, Name: map[string]string{"ru": "Организация 51"}}
	w, _ = request("school.localhost", "PUT", fmt.Sprintf("/api/v1/admin/organizations/%d", first), o, adminCookie, adminCSRF)
	expect(w, 200)
	w, _ = request("51.school.localhost", "GET", "/api/v1/site", nil, "", "")
	expect(w, 404)
	w, _ = request("51.school.localhost", "GET", "/api/v1/auth/me", nil, staffCookie, "")
	expect(w, 404)
	w, _ = request("51.school.localhost", "POST", "/api/v1/auth/login", map[string]string{"login": "school51", "password": reset}, "", "")
	expect(w, 404)
	w, data = request("school.localhost", "GET", "/api/v1/catalog", nil, "", "")
	expect(w, 200)
	if data["total"] != float64(1) {
		t.Fatal("disabled organization in catalog")
	}
	w, _ = request("school.localhost", "POST", "/api/v1/auth/logout", nil, adminCookie, adminCSRF)
	expect(w, 204)
	w, _ = request("school.localhost", "GET", "/api/v1/auth/me", nil, adminCookie, "")
	expect(w, 401)
	files, e := os.ReadDir(a.Uploads)
	if e != nil || len(files) == 0 {
		t.Fatal("upload not saved", e)
	}
	for _, f := range files {
		if filepath.IsAbs(f.Name()) {
			t.Fatal("absolute path")
		}
	}
	// Demo seeding adds complete fixtures and must preserve edits and credentials on repeat.
	t.Chdir("../..")
	if err := a.Command([]string{"seed-demo"}); err != nil {
		t.Fatal(err)
	}
	counts := func() [4]int {
		var result [4]int
		for i, table := range []string{"organizations", "users", "content", "uploads"} {
			if err := a.DB.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&result[i]); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	seededCounts := counts()
	var demoOrg int64
	if err := a.DB.QueryRow(ctx, "SELECT id FROM organizations WHERE slug='demo-school-51'").Scan(&demoOrg); err != nil {
		t.Fatal(err)
	}
	updatedHash, err := passwordHash("Changed-demo-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.Exec(ctx, "UPDATE users SET password_hash=$1 WHERE login='demo.school'", updatedHash); err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.Exec(ctx, `UPDATE content SET translations=jsonb_set(translations,'{ru,body}','"Edited demo body"') WHERE organization_id=$1 AND kind='about'`, demoOrg); err != nil {
		t.Fatal(err)
	}
	if _, err = a.DB.Exec(ctx, `UPDATE content SET translations=jsonb_set(translations,'{ru,title}','"Демо: день открытых дверей"') WHERE organization_id=$1 AND translations->'ru'->>'title'='Демо: изучаем Шымкент вместе'`, demoOrg); err != nil {
		t.Fatal(err)
	}
	if err = a.Command([]string{"seed-demo"}); err != nil {
		t.Fatal(err)
	}
	if counts() != seededCounts {
		t.Fatal("seeding duplicated records")
	}
	if _, err = a.DB.Exec(ctx, "DELETE FROM uploads WHERE id='demo-school-v2-about-1'"); err != nil {
		t.Fatal(err)
	}
	afterPhotoRemoval := counts()
	if err = a.Command([]string{"seed-demo"}); err != nil {
		t.Fatal(err)
	}
	if counts() != afterPhotoRemoval {
		t.Fatal("removing the cover caused fixture restoration or duplicates")
	}
	var demoBody string
	if err = a.DB.QueryRow(ctx, "SELECT translations->'ru'->>'body' FROM content WHERE organization_id=$1 AND kind='about'", demoOrg).Scan(&demoBody); err != nil || demoBody != "Edited demo body" {
		t.Fatal("seeding overwrote material", err)
	}
	for i, login := range []string{"demo.admin", "demo.school", "demo.preschool", "demo.first-login", "demo.disabled"} {
		password := demoPassword
		if login == "demo.school" {
			password = "Changed-demo-password"
		}
		host := map[string]string{"demo.admin": "school.localhost", "demo.school": "demo-school-51.school.localhost", "demo.preschool": "demo-preschool-7.school.localhost", "demo.first-login": "demo-school-52.school.localhost", "demo.disabled": "demo-disabled.school.localhost"}[login]
		body, _ := json.Marshal(map[string]string{"login": login, "password": password})
		r := httptest.NewRequest("POST", "http://"+host+"/api/v1/auth/login", bytes.NewReader(body))
		r.RemoteAddr = fmt.Sprintf("192.0.2.%d:5000", i+1)
		r.Header.Set("Origin", "http://"+host)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if login == "demo.disabled" {
			expect(w, 404)
			continue
		}
		expect(w, 200)
		cookie := w.Result().Cookies()[0].Value
		w, data := request(host, "GET", "/api/v1/auth/me", nil, cookie, "")
		expect(w, 200)
		if data["must_change"] != (login == "demo.first-login") {
			t.Fatal("incorrect demo password-change flag")
		}
		w, _ = request(host, "GET", "/api/v1/admin/content", nil, cookie, "")
		if login == "demo.first-login" {
			expect(w, 403)
		} else {
			expect(w, 200)
		}
		if login == "demo.preschool" {
			w, _ = request(host, "GET", fmt.Sprintf("/api/v1/admin/content?organization_id=%d", demoOrg), nil, cookie, "")
			expect(w, 403)
		}
	}
	w, data = request("demo-school-51.school.localhost", "GET", "/api/v1/content?kind=gallery", nil, "", "")
	expect(w, 200)
	gallery := data["items"].([]any)
	if len(gallery) != 2 || len(gallery[0].(map[string]any)["files"].([]any)) != 3 {
		t.Fatal("rich demo needs 2 galleries with 3 images")
	}
	w, _ = request("demo-school-51.school.localhost", "GET", "/files/demo-v1-demo-school-51-private-draft-1", nil, "", "")
	expect(w, 404)
	w, _ = request("demo-school-51.school.localhost", "GET", "/files/demo-v1-demo-school-51-schedule-1", nil, "", "")
	expect(w, 200)
	for kind, expected := range map[string]int{"about": 1, "administration": 3, "teachers": 6, "news": 6, "documents": 4, "gallery": 2, "schedule": 1} {
		var count int
		if err = a.DB.QueryRow(ctx, "SELECT count(*) FROM content WHERE organization_id=$1 AND kind=$2 AND published", demoOrg, kind).Scan(&count); err != nil || count != expected {
			t.Fatalf("school %s: count %d want %d: %v", kind, count, expected, err)
		}
	}
	a.Secure = true
	if err = a.seedSchool(); err == nil {
		t.Fatal("rich school seeding allowed in production")
	}
	if err = a.seedDemo(); err == nil {
		t.Fatal("demo seeding allowed in production mode")
	}

}

func TestMenuValidation(t *testing.T) {
	for _, org := range []int64{0, 51} {
		menu := defaultMenu(org)
		if !validMenu(menu, org) {
			t.Fatal("default menu rejected")
		}
		menu[0].Visible = false
		if validMenu(menu, org) {
			t.Fatal("hidden home accepted")
		}
		menu[0].Visible = true
		menu[1].Key = menu[0].Key
		if validMenu(menu, org) {
			t.Fatal("duplicate key accepted")
		}
	}
	if validMenu(defaultMenu(0), 51) {
		t.Fatal("central menu accepted for organization")
	}
	menu := defaultMenu(0)
	menu[1].Label["ru"] = " "
	if validMenu(menu, 0) {
		t.Fatal("blank label accepted")
	}
	for _, login := range []string{"a", " has-space", "has space", "has\nnewline"} {
		if validLogin(login) {
			t.Fatal("invalid login accepted")
		}
	}
	if !validLogin("school.staff") {
		t.Fatal("valid login rejected")
	}
}

func TestNestedMenuValidation(t *testing.T) {
	for _, org := range []int64{0, 51} {
		base := defaultMenu(org)
		if !validMenu(base, org) {
			t.Fatal("nested defaults rejected")
		}
		mutate := func(change func([]MenuItem)) []MenuItem {
			copy := append([]MenuItem(nil), base...)
			change(copy)
			return copy
		}
		find := func(items []MenuItem, key string) int {
			for i, item := range items {
				if item.Key == key {
					return i
				}
			}
			t.Fatal("missing key", key)
			return 0
		}
		group := "group-information"
		if org != 0 {
			group = "group-school"
		}
		for name, menu := range map[string][]MenuItem{
			"self cycle":     mutate(func(items []MenuItem) { i := find(items, group); items[i].Parent = group }),
			"missing parent": mutate(func(items []MenuItem) { items[find(items, "news")].Parent = "group-missing" }),
			"leaf parent":    mutate(func(items []MenuItem) { items[find(items, "news")].Parent = "documents" }),
			"nested home":    mutate(func(items []MenuItem) { items[0].Parent = group }),
		} {
			if validMenu(menu, org) {
				t.Fatal("accepted", name)
			}
		}
		nested := append(base, MenuItem{Key: "group-child", Label: map[string]string{"kk": "Ішкі бөлім", "ru": "Подраздел"}, Visible: true, Group: true, Parent: group})
		nested[find(nested, "news")].Parent = "group-child"
		if !validMenu(nested, org) {
			t.Fatal("third level rejected")
		}
		nested = append(nested, MenuItem{Key: "group-deep", Label: map[string]string{"kk": "Терең бөлім", "ru": "Глубже"}, Visible: true, Group: true, Parent: "group-child"})
		nested[find(nested, "news")].Parent = "group-deep"
		if validMenu(nested, org) {
			t.Fatal("fourth level accepted")
		}
		legacy := flatMenu(org)
		legacy[1].Label["ru"] = "Своё название"
		legacy[1].Visible = false
		upgraded := nestLegacyMenu(legacy, org)
		if !validMenu(upgraded, org) {
			t.Fatal("upgraded menu rejected")
		}
		item := upgraded[find(upgraded, legacy[1].Key)]
		if item.Label["ru"] != "Своё название" || item.Visible {
			t.Fatal("legacy edit overwritten")
		}
	}
}

func TestMenuLinks(t *testing.T) {
	for _, org := range []int64{0, 51} {
		for _, address := range []string{"/news", "/documents?page=2#list", "https://example.kz/ru/", "http://example.kz"} {
			menu := defaultMenu(org)
			menu = append(menu, MenuItem{Key: "link-example", Label: map[string]string{"kk": "Сілтеме", "ru": "Ссылка"}, Visible: true, URL: address})
			if !validMenu(menu, org) {
				t.Fatalf("valid URL rejected: %q", address)
			}
			menu[0].URL = address
			if !validMenu(menu, org) {
				t.Fatalf("standard URL override rejected: %q", address)
			}
		}
		for _, address := range []string{"", "javascript:alert(1)", "data:text/html,test", "//example.kz", "/\\example.kz", "https://", "https://user:pass@example.kz", "https://example.kz\n", "news"} {
			menu := append(defaultMenu(org), MenuItem{Key: "link-example", Label: map[string]string{"kk": "Сілтеме", "ru": "Ссылка"}, Visible: true, URL: address})
			if validMenu(menu, org) {
				t.Fatalf("unsafe or missing URL accepted: %q", address)
			}
		}
		menu := defaultMenu(org)
		for i := range menu {
			if menu[i].Group {
				menu[i].URL = "/news"
				break
			}
		}
		if validMenu(menu, org) {
			t.Fatal("group URL accepted")
		}
	}
}
