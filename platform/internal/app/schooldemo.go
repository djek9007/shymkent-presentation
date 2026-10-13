package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Populate only the known local demo school; an attachment marks the completed version.
func (a *App) seedSchool() error {
	if a.Secure || (a.Domain != "localhost" && !strings.HasSuffix(a.Domain, ".localhost")) {
		return errors.New("seed-school разрешён только с DEV_HTTP=1 на локальном домене")
	}
	var fixture struct {
		Name      map[string]string `json:"name"`
		Profile   map[string]string `json:"profile"`
		Materials []struct {
			Key    string   `json:"key"`
			Kind   string   `json:"kind"`
			RU     string   `json:"title_ru"`
			KK     string   `json:"title_kk"`
			BodyRU string   `json:"body_ru"`
			BodyKK string   `json:"body_kk"`
			Assets []string `json:"assets"`
			Date   string   `json:"date"`
		} `json:"materials"`
	}
	raw, err := os.ReadFile("fixtures/demo/school/content.json")
	if err != nil {
		return err
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		return err
	}
	ctx := context.Background()
	tx, err := a.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var org int64
	if err = tx.QueryRow(ctx, "SELECT id FROM organizations WHERE slug='demo-school-51' FOR UPDATE").Scan(&org); err != nil {
		return err
	}
	var done bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM uploads WHERE id LIKE 'demo-school-v2-%' AND organization_id=$1)", org).Scan(&done); err != nil {
		return err
	}
	if done {
		return nil
	}
	created := []string{}
	committed := false
	defer func() {
		if !committed {
			for _, path := range created {
				_ = os.Remove(path)
			}
		}
	}()
	if _, err = tx.Exec(ctx, "UPDATE organizations SET name=$1,profile=profile||$2::jsonb WHERE id=$3", fixture.Name, fixture.Profile, org); err != nil {
		return err
	}
	// Keep the original fixtures (including private drafts) for permission tests, but replace their public presentation.
	if _, err = tx.Exec(ctx, `UPDATE content SET published=false WHERE organization_id=$1 AND kind NOT IN ('about','schedule') AND translations->'ru'->>'title' LIKE 'Демо:%'`, org); err != nil {
		return err
	}
	for position, m := range fixture.Materials {
		if !kinds[m.Kind] || len(m.Assets) == 0 {
			return fmt.Errorf("invalid school fixture %s", m.Key)
		}
		translations := map[string]Translation{"ru": {Title: m.RU, Body: a.Policy.Sanitize(m.BodyRU)}, "kk": {Title: m.KK, Body: a.Policy.Sanitize(m.BodyKK)}}
		var cid int64
		if m.Kind == "about" || m.Kind == "schedule" {
			err = tx.QueryRow(ctx, "SELECT id FROM content WHERE organization_id=$1 AND kind=$2", org, m.Kind).Scan(&cid)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if cid != 0 {
			_, err = tx.Exec(ctx, "UPDATE content SET translations=$1,published=true,position=$2,updated_at=$3::timestamptz,schedule_date=CASE WHEN kind='schedule' THEN $3::date ELSE schedule_date END WHERE id=$4", translations, position, m.Date, cid)
		} else {
			err = tx.QueryRow(ctx, "INSERT INTO content(organization_id,kind,published,position,translations,updated_at) VALUES($1,$2,true,$3,$4,$5::timestamptz) RETURNING id", org, m.Kind, position, translations, m.Date).Scan(&cid)
		}
		if err != nil {
			return err
		}
		for index, asset := range m.Assets {
			if filepath.Base(asset) != asset {
				return errors.New("invalid fixture path")
			}
			data, err := os.ReadFile(filepath.Join("fixtures/demo/school", asset))
			if err != nil {
				return err
			}
			mime, ok := fileType(asset, data)
			if !ok {
				return fmt.Errorf("invalid school asset %s", asset)
			}
			id := fmt.Sprintf("demo-school-v2-%s-%d", m.Key, index+1)
			relative := id + filepath.Ext(asset)
			full := filepath.Join(a.Uploads, relative)
			out, err := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err == nil {
				created = append(created, full)
				_, we := out.Write(data)
				ce := out.Close()
				if we != nil {
					return we
				}
				if ce != nil {
					return ce
				}
			} else if !os.IsExist(err) {
				return err
			}
			var thumbnail any
			w, h := 0, 0
			caption := map[string]string{"ru": "Учебный образец PDF · тестовая школа", "kk": "PDF оқу үлгісі · тест мектебі"}
			if strings.HasPrefix(mime, "image/") {
				cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
				if err != nil {
					return err
				}
				w, h = cfg.Width, cfg.Height
				thumbnail = relative
				caption = map[string]string{"ru": "Фото Unsplash · иллюстрация для тестовой школы", "kk": "Unsplash фотосы · тест мектебіне арналған иллюстрация"}
			}
			if m.Kind == "schedule" {
				// Preserve the original attachment ID so repeated seed-demo does not restore the old PDF.
				result, e := tx.Exec(ctx, `UPDATE uploads SET name=$1,mime=$2,path=$3,thumbnail=NULL,size=$4,caption=$5 WHERE content_id=$6 AND id=$7`, "Расписание 7А класса · образец.pdf", mime, relative, len(data), caption, cid, "demo-v1-demo-school-51-schedule-1")
				if e != nil {
					return e
				}
				if result.RowsAffected() == 1 {
					continue
				}
			}
			_, err = tx.Exec(ctx, `INSERT INTO uploads(id,organization_id,content_id,purpose,name,mime,path,thumbnail,size,caption,position,image_width,image_height) VALUES($1,$2,$3,'content',$4,$5,$6,$7,$8,$9,$10,$11,$12)`, id, org, cid, asset, mime, relative, thumbnail, len(data), caption, index, w, h)
			if err != nil {
				return err
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	committed = true
	fmt.Println("Демонстрационная школа №51 заполнена: сведения, 3 администратора, 6 педагогов, 6 новостей, 4 документа, 2 альбома и расписание.")
	return nil
}
