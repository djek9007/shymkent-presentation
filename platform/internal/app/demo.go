package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Public fixture credential, intentionally usable only with DEV_HTTP and a localhost domain.
const demoPassword = "Demo-Access-2026!"

func (a *App) seedDemo() error {
	if a.Secure || (a.Domain != "localhost" && !strings.HasSuffix(a.Domain, ".localhost")) {
		return errors.New("seed-demo разрешён только с DEV_HTTP=1 и локальным BASE_DOMAIN")
	}
	ctx := context.Background()
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(20261011)"); err != nil {
		return err
	}
	password, err := passwordHash(demoPassword)
	if err != nil {
		return err
	}
	account := func(login string, org int64, firstLogin bool) error {
		_, e := tx.Exec(ctx, "INSERT INTO users(organization_id,login,password_hash,must_change) VALUES($1,$2,$3,$4) ON CONFLICT(login) DO NOTHING", nullable(org), login, password, firstLogin)
		if e != nil {
			return e
		}
		var owner int64
		if e = tx.QueryRow(ctx, "SELECT COALESCE(organization_id,0) FROM users WHERE login=$1", login).Scan(&owner); e != nil {
			return e
		}
		if owner != org {
			return fmt.Errorf("логин %s уже принадлежит другой организации; изменения отменены", login)
		}
		return nil
	}
	if err = account("demo.admin", 0, false); err != nil {
		return err
	}
	createdFiles := []string{}
	committed := false
	defer func() {
		if !committed {
			for _, p := range createdFiles {
				_ = os.Remove(p)
			}
		}
	}()
	file := func(org, cid int64, key, asset, purpose string, position int) error {
		id := "demo-v1-" + key
		relative := id + filepath.Ext(asset)
		data, e := os.ReadFile(filepath.Join("fixtures", "demo", asset))
		if e != nil {
			return e
		}
		mt, ok := fileType(asset, data)
		if !ok {
			return fmt.Errorf("invalid fixture: %s", asset)
		}
		full := filepath.Join(a.Uploads, relative)
		f, e := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e == nil {
			createdFiles = append(createdFiles, full)
			_, e = f.Write(data)
			closeErr := f.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
		} else if !os.IsExist(e) {
			return e
		}
		caption := map[string]string{"kk": "Тест құжаты", "ru": "Тестовый документ"}
		width, height := 0, 0
		var thumb any
		if strings.HasPrefix(mt, "image/") {
			caption = map[string]string{"kk": "Демо сурет - нақты ұйым емес", "ru": "Демонстрация - вымышленная организация"}
			width, height = 720, 480
			thumb = relative
			if asset == "logo.png" {
				width, height = 200, 200
			}
		}
		_, e = tx.Exec(ctx, `INSERT INTO uploads(id,organization_id,content_id,purpose,name,mime,path,thumbnail,size,caption,position,image_width,image_height) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(id) DO NOTHING`, id, nullable(org), nullable(cid), purpose, "Демо - "+asset, mt, relative, thumb, len(data), caption, position, width, height)
		if e != nil {
			return e
		}
		var owner, attached int64
		var existingPurpose string
		if e = tx.QueryRow(ctx, "SELECT COALESCE(organization_id,0),COALESCE(content_id,0),purpose FROM uploads WHERE id=$1", id).Scan(&owner, &attached, &existingPurpose); e != nil {
			return e
		}
		if owner != org || attached != cid || existingPurpose != purpose {
			return fmt.Errorf("fixture %s уже используется другой записью", id)
		}
		return nil
	}
	type material struct {
		key, kind, kk, ru, bodyKK, bodyRU string
		published                         bool
		assets                            []string
	}
	legacyTitles := map[string]string{"welcome": "Демо: единый портал образования", "open-day": "Демо: день открытых дверей", "no-translation": "Демо: новость без казахского перевода", "album": "Демо: жизнь организации"}
	insert := func(org int64, prefix string, m material, position int) error {
		var id int64
		// shortcut: fixture titles identify seed records; keep their titles when rerunning seed-demo.
		e := tx.QueryRow(ctx, "SELECT id FROM content WHERE COALESCE(organization_id,0)=$1 AND kind=$2 AND (translations->'ru'->>'title'=$3 OR ($4<>'' AND translations->'ru'->>'title'=$4)) ORDER BY id LIMIT 1", org, m.kind, m.ru, legacyTitles[m.key]).Scan(&id)
		if errors.Is(e, pgx.ErrNoRows) {
			if m.kind == "about" || m.kind == "schedule" {
				e = tx.QueryRow(ctx, "SELECT id FROM content WHERE COALESCE(organization_id,0)=$1 AND kind=$2", org, m.kind).Scan(&id)
			}
			if errors.Is(e, pgx.ErrNoRows) {
				translations := map[string]Translation{"ru": {Title: m.ru, Body: m.bodyRU}}
				if m.kk != "" {
					translations["kk"] = Translation{Title: m.kk, Body: m.bodyKK}
				}
				var date any
				if m.kind == "schedule" {
					date = time.Now().Format("2006-01-02")
				}
				e = tx.QueryRow(ctx, "INSERT INTO content(organization_id,kind,published,position,translations,schedule_date) VALUES($1,$2,$3,$4,$5,$6::date) RETURNING id", nullable(org), m.kind, m.published, position, translations, date).Scan(&id)
			}
		}
		if e != nil {
			return e
		}
		for i, asset := range m.assets {
			if e = file(org, id, fmt.Sprintf("%s-%s-%d", prefix, m.key, i+1), asset, "content", i); e != nil {
				return e
			}
		}
		return nil
	}
	portal := []material{
		{"welcome", "news", "Демо: Шымкенттің білім кеңістігі", "Демо: образование Шымкента", "<p>Шымкент мектептері мен балабақшаларына арналған бірыңғай білім кеңістігі. Ұйымды каталогтан тауып, жаңалықтар мен құжаттарды қараңыз. Бұл - демонстрациялық материал.</p>", "<p>Единое пространство школ и детских садов Шымкента: новости, документы и контакты организаций города. Знакомимся с родным городом, учимся и заботимся о его зелёных пространствах. Демонстрационный материал.</p>", true, []string{"campus.png"}},
		{"draft", "news", "Демо: жарияланбаған хабарландыру", "Демо: неопубликованное объявление", "<p>Бұл жоба тек әкімшіге қолжетімді.</p>", "<p>Этот черновик видит только центральный администратор.</p>", false, nil},
		{"document", "documents", "Демо: портал ережелері", "Демо: правила портала", "<p>Жүктеу мен алдын ала қарауды тексеруге арналған құжат.</p>", "<p>Тестовый документ для проверки скачивания и предпросмотра.</p>", true, []string{"document.pdf"}},
	}
	for i, m := range portal {
		if err = insert(0, "portal", m, i); err != nil {
			return err
		}
	}
	organizations := []struct {
		slug, kind, kk, ru, login string
		active, firstLogin        bool
	}{
		{"demo-school-51", "school", "Демо: Шымкент, №51 мектеп", "Демо: Шымкент, школа №51", "demo.school", true, false},
		{"demo-preschool-7", "preschool", "Демо: Шымкент, №7 балабақша", "Демо: Шымкент, детский сад №7", "demo.preschool", true, false},
		{"demo-school-52", "school", "Демо: Шымкент, №52 мектеп", "Демо: Шымкент, школа №52", "demo.first-login", true, true},
		{"demo-disabled", "school", "Демо: Шымкент, өшірілген мектеп", "Демо: Шымкент, отключённая школа", "demo.disabled", false, false},
	}
	materials := []material{
		{"about", "about", "Демо: ұйым туралы", "Демо: об организации", "<h2>Шымкенттегі білім кеңістігі</h2><p>Бұл - Шымкент қаласына арналған ойдан шығарылған білім беру ұйымы.</p><ul><li>Қазақ және орыс тілдеріндегі білім</li><li>Туған өлкенің тарихы мен мәдениеті</li><li>Қала табиғатына қамқорлық және оқу жобалары</li></ul>", "<h2>Образовательное пространство Шымкента</h2><p>Вымышленная образовательная организация для демонстрации городского портала.</p><ul><li>Обучение на казахском и русском языках</li><li>История и культура родного города</li><li>Забота о природе и учебные проекты</li></ul>", true, nil},
		{"director", "administration", "Демо: директор", "Демо: директор", "<p>Тест директоры. Қабылдау: дүйсенбі, 15:00-17:00.</p>", "<p>Тестовый директор. Приём: понедельник, 15:00-17:00.</p>", true, nil},
		{"deputy", "administration", "Демо: директор орынбасары", "Демо: заместитель директора", "<p>Оқу жұмысы бойынша тест орынбасары.</p>", "<p>Тестовый заместитель директора по учебной работе.</p>", true, nil},
		{"teacher-math", "teachers", "Демо: математика педагогі", "Демо: педагог математики", "<p>Тест педагогі. Математика және жобалық сабақтар.</p>", "<p>Тестовый педагог. Математика и проектные занятия.</p>", true, nil},
		{"teacher-language", "teachers", "Демо: тіл педагогі", "Демо: педагог языков", "<p>Қазақ және орыс тілдері бойынша тест педагогі.</p>", "<p>Тестовый педагог казахского и русского языков.</p>", true, nil},
		{"open-day", "news", "Демо: Шымкентті бірге танимыз", "Демо: изучаем Шымкент вместе", "<p>Туған қаламызды танимыз: Шымкенттің тарихы, Қошқар ата өзені және дендросаябақ туралы оқу жобалары.</p><p>Бұл - ойдан шығарылған мектеп жобасының үлгісі, нақты іс-шара туралы хабарландыру емес.</p>", "<p>Учебные проекты о родном городе: история Шымкента, река Қошқар ата и дендропарк. Исследуем городские пространства и обсуждаем бережное отношение к природе.</p><p>Это пример вымышленного школьного проекта, не объявление о реальном мероприятии.</p>", true, []string{"campus.png"}},
		{"no-translation", "news", "", "Демо: краеведение - река Қошқар ата", "", "<p>Пример занятия по краеведению о реке Қошқар ата в Шымкенте: городской ландшафт, природа и забота о воде.</p><p>Демонстрационный материал. Казахский перевод пока не добавлен.</p>", true, nil},
		{"private-draft", "news", "Демо: құпия жоба", "Демо: закрытый черновик", "<p>Жоба мен оның суреті тек осы ұйым қызметкеріне және әкімшіге қолжетімді.</p>", "<p>Черновик и его изображение доступны только сотруднику этой организации и центральному администратору.</p>", false, []string{"classroom.png"}},
		{"rules", "documents", "Демо: ұйым ережелері", "Демо: правила организации", "<p>Тест құжаты. Заңды күші жоқ.</p>", "<p>Демонстрационный документ без юридической силы.</p>", true, []string{"document.pdf"}},
		{"album", "gallery", "Демо: Шымкент - біздің қаламыз", "Демо: Шымкент - наш город", "<p>Шымкенттегі білім мен табиғат тақырыбындағы үш шартты иллюстрация. Бұл нақты ұйымның фотосуреттері емес.</p>", "<p>Три условные иллюстрации на тему образования и зелёных пространств Шымкента. Это не фотографии реальной организации.</p>", true, []string{"campus.png", "classroom.png", "garden.png"}},
		{"schedule", "schedule", "Демо: сабақ кестесі", "Демо: расписание занятий", "<p>Тест кестесі. PDF файлын ашыңыз немесе жүктеңіз.</p>", "<p>Тестовое расписание. Откройте или скачайте PDF.</p>", true, []string{"schedule.pdf"}},
	}
	for _, o := range organizations {
		hidden := []string{}
		if o.kind == "preschool" {
			hidden = append(hidden, "schedule")
		}
		profile := map[string]string{"address_kk": "Шымкент, Демо көшесі, 1 (тест)", "address_ru": "Шымкент, улица Демо, 1 (тест)", "summary_kk": "Шымкенттегі білім, туған өлкені тану және табиғатқа қамқорлық. Демонстрациялық ұйым: барлық мәліметтер ойдан шығарылған.", "summary_ru": "Образование в Шымкенте, краеведение и забота о природе родного города. Демонстрационная организация: все сведения вымышлены.", "phone": "+7 (000) 000-00-00", "email": o.slug + "@example.invalid", "map_url": "https://www.openstreetmap.org/?mlat=42.3155&mlon=69.5869#map=13/42.3155/69.5869", "logo": "demo-v1-" + o.slug + "-logo"}
		var id int64
		err = tx.QueryRow(ctx, "INSERT INTO organizations(slug,kind,active,name,profile,hidden_sections) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(slug) DO NOTHING RETURNING id", o.slug, o.kind, o.active, map[string]string{"kk": o.kk, "ru": o.ru}, profile, hidden).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, "SELECT id FROM organizations WHERE slug=$1", o.slug).Scan(&id)
		}
		if err != nil {
			return err
		}
		if err = account(o.login, id, o.firstLogin); err != nil {
			return err
		}
		if err = file(id, 0, o.slug+"-logo", "logo.png", "logo", 0); err != nil {
			return err
		}
		for i, m := range materials {
			if o.kind == "preschool" && m.kind == "schedule" {
				continue
			}
			if err = insert(id, o.slug, m, i); err != nil {
				return err
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	committed = true
	fmt.Println("Демо-данные добавлены. Новые аккаунты: demo.admin, demo.school, demo.preschool, demo.first-login, demo.disabled.")
	fmt.Println("Пароль новых аккаунтов: " + demoPassword + ". Пароли и материалы существующих аккаунтов не изменены.")
	return nil
}
