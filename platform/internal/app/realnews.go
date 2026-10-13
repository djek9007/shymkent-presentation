package app

import (
	"context"
	"errors"
	"image"
	_ "image/jpeg"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Local sample portal content: short summaries, original publication dates and attribution.
func (a *App) seedNews() error {
	if a.Secure || (a.Domain != "localhost" && !strings.HasSuffix(a.Domain, ".localhost")) {
		return errors.New("seed-news разрешён только на локальном демонстрационном портале")
	}
	articles := []struct{ key, date, ru, kk, bodyRU, bodyKK, url, asset, caption string }{
		{"knowledge-day", "2026-09-16", "День знаний в школе №59 Шымкента", "Шымкенттің №59 мектебіндегі Білім күні", "В школе №59 состоялась торжественная линейка к началу 2026–2027 учебного года. Праздник объединил учеников, учителей и родителей.", "№59 мектепте 2026–2027 оқу жылының басталуына арналған салтанатты жиын өтті. Мерекеге оқушылар, мұғалімдер мен ата-аналар қатысты.", "https://59-shymkent.edu.kz/main/533-1-yrkjek-blm-kn.html", "knowledge-day.jpg", "День знаний · школа №59, Шымкент / Білім күні · №59 мектеп"},
		{"school-preparation", "2026-07-29", "Школа №59 готовится к новому учебному году", "№59 мектептің жаңа оқу жылына дайындығы", "Школа №59 сообщила о подготовке к 2026–2027 учебному году. Комиссия изучила состояние учебных кабинетов, спортивного зала, столовой и материальной базы школы.", "№59 мектеп 2026–2027 оқу жылына дайындық туралы хабарлады. Комиссия оқу кабинеттерін, спорт залын, асхананы және мектептің материалдық базасын зерделеді.", "https://59-shymkent.edu.kz/main/531-59-zhalpy-orta-blm-beretn-mektept-zhaa-ou-zhylyna-dajyndyy.html", "school-preparation.jpg", "Подготовка к учебному году · школа №59 / Оқу жылына дайындық · №59 мектеп"},
		{"summer-camp", "2026-07-22", "Праздник детства на площадке «Балдәурен»", "«Балдәурен» алаңындағы балалық шақ мерекесі", "Школа №59 рассказала о празднике к Международному дню защиты детей на пришкольной оздоровительной площадке «Балдәурен». Мероприятие состоялось 1 июня; заметка опубликована 22 июля.", "№59 мектеп жанындағы «Балдәурен» сауықтыру алаңында Балаларды қорғау күніне арналған мереке өтті. Іс-шара 1 маусымда өтті, ақпарат 22 шілдеде жарияланды.", "https://59-shymkent.edu.kz/main/527-baytty-balaly-sha-meken-balduren-alaynda-mereke-tt-.html", "", ""},
	}
	ctx := context.Background()
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(20261012)"); err != nil {
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
	for i, article := range articles {
		var cid int64
		err = tx.QueryRow(ctx, "SELECT id FROM content WHERE organization_id IS NULL AND kind='news' AND translations->'ru'->>'title'=$1", article.ru).Scan(&cid)
		if errors.Is(err, pgx.ErrNoRows) {
			translations := map[string]Translation{"ru": {Title: article.ru, Body: "<p>" + article.bodyRU + "</p><p><a href=\"" + article.url + "\">Источник: школа №59 Шымкента →</a></p>"}, "kk": {Title: article.kk, Body: "<p>" + article.bodyKK + "</p><p><a href=\"" + article.url + "\">Дереккөз: Шымкенттің №59 мектебі →</a></p>"}}
			err = tx.QueryRow(ctx, "INSERT INTO content(kind,published,position,translations,updated_at) VALUES('news',true,$1,$2,$3::timestamptz) RETURNING id", i-30, translations, article.date+"T12:00:00+05:00").Scan(&cid)
		}
		if err != nil {
			return err
		}
		if article.asset == "" {
			continue
		}
		id := "school59-" + article.key
		var attached int64
		err = tx.QueryRow(ctx, "SELECT content_id FROM uploads WHERE id=$1", id).Scan(&attached)
		if err == nil {
			if attached != cid {
				return errors.New("исходная фотография уже прикреплена к другой записи; сохраните заголовок при повторном seed-news")
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		data, err := os.ReadFile(filepath.Join("fixtures", "demo", article.asset))
		if err != nil {
			return err
		}
		f, err := os.Open(filepath.Join("fixtures", "demo", article.asset))
		if err != nil {
			return err
		}
		cfg, _, err := image.DecodeConfig(f)
		f.Close()
		if err != nil {
			return err
		}
		relative := id + ".jpg"
		full := filepath.Join(a.Uploads, relative)
		out, err := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			createdFiles = append(createdFiles, full)
			_, writeErr := out.Write(data)
			closeErr := out.Close()
			if writeErr != nil {
				return writeErr
			}
			if closeErr != nil {
				return closeErr
			}
		} else if !os.IsExist(err) {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO uploads(id,content_id,purpose,name,mime,path,thumbnail,size,caption,image_width,image_height) VALUES($1,$2,'content',$3,'image/jpeg',$4,$4,$5,$6,$7,$8)`, id, cid, article.asset, relative, len(data), map[string]string{"ru": article.caption, "kk": article.caption}, cfg.Width, cfg.Height)
		if err != nil {
			return err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	committed = true
	return nil
}
