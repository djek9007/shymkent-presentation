package app

import (
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/url"
	"strings"
	"unicode"
)

type MenuItem struct {
	Key     string            `json:"key"`
	Label   map[string]string `json:"label"`
	Visible bool              `json:"visible"`
	Parent  string            `json:"parent"`
	Group   bool              `json:"group,omitempty"`
	URL     string            `json:"url,omitempty"`
}

func flatMenu(org int64) []MenuItem {
	items := []MenuItem{{Key: "home", Label: map[string]string{"kk": "Басты бет", "ru": "Главная"}, Visible: true}}
	definitions := [][3]string{{"catalog", "Ұйымдар", "Организации"}, {"news", "Жаңалықтар", "Новости"}, {"documents", "Құжаттар", "Документы"}}
	if org != 0 {
		definitions = [][3]string{{"about", "Ұйым туралы", "Об организации"}, {"administration", "Әкімшілік", "Администрация"}, {"teachers", "Педагогтер", "Педагоги"}, {"news", "Жаңалықтар", "Новости"}, {"documents", "Құжаттар", "Документы"}, {"gallery", "Фотогалерея", "Галерея"}, {"schedule", "Кесте", "Расписание"}, {"contacts", "Байланыс", "Контакты"}}
	}
	for _, s := range definitions {
		items = append(items, MenuItem{Key: s[0], Label: map[string]string{"kk": s[1], "ru": s[2]}, Visible: true})
	}
	return items
}
func defaultMenu(org int64) []MenuItem { return nestLegacyMenu(flatMenu(org), org) }

// Upgrade the old flat format without changing labels, visibility or sibling order.
func nestLegacyMenu(items []MenuItem, org int64) []MenuItem {
	for _, item := range items {
		if item.Group || item.Parent != "" {
			return items
		}
	}
	parents := map[string]string{"news": "group-information", "documents": "group-information"}
	groups := map[string][2]string{"group-information": {"Ақпарат", "Информация"}}
	if org != 0 {
		parents = map[string]string{"about": "group-school", "administration": "group-school", "teachers": "group-school", "schedule": "group-learning", "documents": "group-learning", "news": "group-life", "gallery": "group-life"}
		groups = map[string][2]string{"group-school": {"Мектеп туралы", "О школе"}, "group-learning": {"Оқу процесі", "Учебный процесс"}, "group-life": {"Мектеп өмірі", "Школьная жизнь"}}
	}
	result := []MenuItem{}
	added := map[string]bool{}
	for _, item := range items {
		if parent := parents[item.Key]; parent != "" {
			if !added[parent] {
				label := groups[parent]
				result = append(result, MenuItem{Key: parent, Label: map[string]string{"kk": label[0], "ru": label[1]}, Visible: true, Group: true})
				added[parent] = true
			}
			item.Parent = parent
		}
		result = append(result, item)
	}
	return result
}
func validMenuURL(value string) bool {
	if len(value) > 2048 || strings.ContainsAny(value, "\\") || strings.IndexFunc(value, unicode.IsSpace) >= 0 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	if strings.HasPrefix(value, "/") {
		return !strings.HasPrefix(value, "//") && parsed.Host == "" && parsed.Scheme == ""
	}
	return (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Hostname() != "" && parsed.User == nil
}
func validMenu(items []MenuItem, org int64) bool {
	required := map[string]bool{}
	for _, item := range flatMenu(org) {
		required[item.Key] = true
	}
	if len(items) < len(required) || len(items) > 50 {
		return false
	}
	byKey := map[string]MenuItem{}
	for _, item := range items {
		if _, exists := byKey[item.Key]; exists {
			return false
		}
		if item.Group {
			if item.URL != "" || !strings.HasPrefix(item.Key, "group-") || !slugRE.MatchString(item.Key) {
				return false
			}
		} else {
			if required[item.Key] {
				delete(required, item.Key)
			} else if !strings.HasPrefix(item.Key, "link-") || !slugRE.MatchString(item.Key) || item.URL == "" {
				return false
			}
			if item.URL != "" && !validMenuURL(item.URL) {
				return false
			}
		}
		if len(item.Label) != 2 {
			return false
		}
		for _, l := range []string{"kk", "ru"} {
			if strings.TrimSpace(item.Label[l]) == "" || len(item.Label[l]) > 160 {
				return false
			}
		}
		if item.Key == "home" && (!item.Visible || item.Parent != "") {
			return false
		}
		byKey[item.Key] = item
	}
	if len(required) != 0 {
		return false
	}
	for _, item := range items {
		seen := map[string]bool{item.Key: true}
		depth := 1
		for parent := item.Parent; parent != ""; {
			ancestor, exists := byKey[parent]
			if !exists || !ancestor.Group || seen[parent] {
				return false
			}
			seen[parent] = true
			depth++
			if depth > 3 {
				return false
			}
			parent = ancestor.Parent
		}
	}
	return true
}
func (a *App) readMenu(r *http.Request, org int64) ([]MenuItem, error) {
	items := defaultMenu(org)
	var saved []MenuItem
	var legacy bool
	err := a.DB.QueryRow(r.Context(), "SELECT menu,NOT EXISTS(SELECT 1 FROM jsonb_array_elements(menu) entry WHERE entry ? 'parent') FROM site_settings WHERE site_id=$1", org).Scan(&saved, &legacy)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		if legacy {
			return nestLegacyMenu(saved, org), nil
		}
		return saved, nil
	}
	if org != 0 {
		var hidden []string
		if err = a.DB.QueryRow(r.Context(), "SELECT hidden_sections FROM organizations WHERE id=$1", org).Scan(&hidden); err != nil {
			return nil, err
		}
		for i := range items {
			for _, key := range hidden {
				if items[i].Key == key {
					items[i].Visible = false
				}
			}
		}
	}
	return items, nil
}
func (a *App) menu(w http.ResponseWriter, r *http.Request) {
	org, ok := requestOrg(w, r)
	if !ok {
		return
	}
	if org != 0 {
		var exists bool
		if err := a.DB.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM organizations WHERE id=$1)", org).Scan(&exists); err != nil {
			a.dbError(w, err)
			return
		}
		if !exists {
			fail(w, 404, "Организация не найдена")
			return
		}
	}
	if r.Method == "PUT" {
		var items []MenuItem
		if !decode(w, r, &items) {
			return
		}
		if !validMenu(items, org) {
			fields(w, map[string]string{"menu": "Укажите каждый раздел один раз и названия ҚАЗ/РУС. До 3 уровней; родителем может быть только группа. Циклы запрещены, главная всегда на верхнем уровне. URL: путь от / или полный адрес http:// / https://; дополнительным ссылкам нужен адрес, группам — без URL."})
			return
		}
		tx, err := a.DB.Begin(r.Context())
		if err != nil {
			a.dbError(w, err)
			return
		}
		defer tx.Rollback(r.Context())
		if org != 0 {
			hidden := []string{}
			for _, item := range items {
				if !item.Visible && kinds[item.Key] {
					hidden = append(hidden, item.Key)
				}
			}
			if _, err = tx.Exec(r.Context(), "UPDATE organizations SET hidden_sections=$1 WHERE id=$2", hidden, org); err != nil {
				a.dbError(w, err)
				return
			}
		}
		if _, err = tx.Exec(r.Context(), "INSERT INTO site_settings(site_id,menu) VALUES($1,$2) ON CONFLICT(site_id) DO UPDATE SET menu=EXCLUDED.menu", org, items); err != nil {
			a.dbError(w, err)
			return
		}
		if err = tx.Commit(r.Context()); err != nil {
			a.dbError(w, err)
			return
		}
		write(w, 200, items)
		return
	}
	items, err := a.readMenu(r, org)
	if err != nil {
		a.dbError(w, err)
		return
	}
	write(w, 200, items)
}
