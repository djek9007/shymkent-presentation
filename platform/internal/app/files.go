package app

import (
	"archive/zip"
	"bytes"
	"github.com/jackc/pgx/v5"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Upload struct {
	Width     int               `json:"width"`
	Height    int               `json:"height"`
	ID        string            `json:"id"`
	Org       int64             `json:"organization_id"`
	ContentID int64             `json:"content_id"`
	Purpose   string            `json:"purpose"`
	Name      string            `json:"name"`
	MIME      string            `json:"mime"`
	Path      string            `json:"-"`
	Thumbnail string            `json:"-"`
	Size      int64             `json:"size"`
	Caption   map[string]string `json:"caption"`
	Position  int               `json:"position"`
}

const uploadColumns = "id,COALESCE(organization_id,0),COALESCE(content_id,0),purpose,name,mime,path,COALESCE(thumbnail,''),size,caption,position,image_width,image_height"

func scanUpload(row pgx.Row) (Upload, error) {
	f := Upload{}
	e := row.Scan(&f.ID, &f.Org, &f.ContentID, &f.Purpose, &f.Name, &f.MIME, &f.Path, &f.Thumbnail, &f.Size, &f.Caption, &f.Position, &f.Width, &f.Height)
	return f, e
}
func (a *App) attachFiles(r *http.Request, c *Content) error {
	rows, e := a.DB.Query(r.Context(), "SELECT "+uploadColumns+" FROM uploads WHERE content_id=$1 ORDER BY position,created_at", c.ID)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		f, e := scanUpload(rows)
		if e != nil {
			return e
		}
		c.Files = append(c.Files, f)
	}
	return rows.Err()
}
func fileType(name string, b []byte) (string, bool) {
	ext := strings.ToLower(filepath.Ext(name))
	detected := http.DetectContentType(b)
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg", detected == "image/jpeg"
	case ".png":
		return "image/png", detected == "image/png"
	case ".webp":
		return "image/webp", detected == "image/webp"
	case ".pdf":
		return "application/pdf", bytes.HasPrefix(b, []byte("%PDF-"))
	case ".docx", ".xlsx":
		z, e := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if e != nil {
			return "", false
		}
		prefix := "word/"
		kind := "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
		if ext == ".xlsx" {
			prefix = "xl/"
			kind = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
		}
		content, main := false, false
		for _, f := range z.File {
			if f.Name == "[Content_Types].xml" {
				content = true
			}
			if f.Name == prefix+"document.xml" || f.Name == prefix+"workbook.xml" {
				main = true
			}
		}
		return kind, content && main
	}
	return "", false
}
func (a *App) upload(w http.ResponseWriter, r *http.Request) {
	org, ok := requestOrg(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 21<<20)
	if e := r.ParseMultipartForm(1 << 20); e != nil {
		fields(w, map[string]string{"file": "Слишком большой или некорректный файл"})
		return
	}
	defer r.MultipartForm.RemoveAll()
	purpose := r.FormValue("purpose")
	if purpose == "" {
		purpose = "content"
	}
	cid, _ := strconv.ParseInt(r.FormValue("content_id"), 10, 64)
	kind := ""
	if purpose == "content" {
		var owner int64
		e := a.DB.QueryRow(r.Context(), "SELECT COALESCE(organization_id,0),kind FROM content WHERE id=$1", cid).Scan(&owner, &kind)
		if e != nil {
			a.dbError(w, e)
			return
		}
		if !a.allow(w, r, owner) {
			return
		}
		if owner != org {
			fail(w, 403, "Неверная организация")
			return
		}
	} else if purpose == "logo" {
		cid = 0
		if org == 0 {
			fields(w, map[string]string{"file": "Логотип загружается для организации"})
			return
		}
	} else {
		fields(w, map[string]string{"purpose": "Неизвестное назначение"})
		return
	}
	f, h, e := r.FormFile("file")
	if e != nil {
		fields(w, map[string]string{"file": "Выберите файл"})
		return
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (20<<20)+1))
	if e != nil {
		fail(w, 422, "Не удалось прочитать файл")
		return
	}
	mt, valid := fileType(h.Filename, b)
	img := strings.HasPrefix(mt, "image/")
	if !valid || len(b) > 20<<20 || (img && len(b) > 10<<20) || (kind == "schedule" && mt != "application/pdf") || ((purpose == "logo" || kind == "gallery" || kind == "banners") && !img) {
		fields(w, map[string]string{"file": "JPEG/PNG/WebP до 10 МБ, PDF/DOCX/XLSX до 20 МБ; расписание: PDF; баннеры, галерея и логотип: изображения"})
		return
	}
	id := token()
	relative := id + strings.ToLower(filepath.Ext(h.Filename))
	thumb := ""
	imageWidth, imageHeight := 0, 0
	if img {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
		if err != nil || cfg.Width < 1 || cfg.Height < 1 || int64(cfg.Width)*int64(cfg.Height) > 40000000 {
			fields(w, map[string]string{"file": "Некорректное изображение или больше 40 мегапикселей"})
			return
		}
		imageWidth, imageHeight = cfg.Width, cfg.Height
		src, _, err := image.Decode(bytes.NewReader(b))
		if err != nil {
			fields(w, map[string]string{"file": "Изображение не читается"})
			return
		}
		width, height := cfg.Width, cfg.Height
		if width > 720 {
			height = height * 720 / width
			width = 720
		}
		if height > 720 {
			width = width * 720 / height
			height = 720
		}
		if width < 1 {
			width = 1
		}
		if height < 1 {
			height = 1
		}
		dst := image.NewRGBA(image.Rect(0, 0, width, height))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
		thumb = id + "-thumb.png"
		tf, err := os.OpenFile(filepath.Join(a.Uploads, thumb), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			a.dbError(w, err)
			return
		}
		err = png.Encode(tf, dst)
		closeErr := tf.Close()
		if err != nil || closeErr != nil {
			os.Remove(filepath.Join(a.Uploads, thumb))
			fail(w, 500, "Не удалось сохранить изображение")
			return
		}
	}
	cleanup := func() {
		os.Remove(filepath.Join(a.Uploads, relative))
		if thumb != "" {
			os.Remove(filepath.Join(a.Uploads, thumb))
		}
	}
	if e = os.WriteFile(filepath.Join(a.Uploads, relative), b, 0600); e != nil {
		cleanup()
		a.dbError(w, e)
		return
	}
	name := filepath.Base(strings.ReplaceAll(h.Filename, "\\", "/"))
	if len(name) > 250 {
		name = "file" + filepath.Ext(name)
	}
	tx, e := a.DB.Begin(r.Context())
	if e != nil {
		cleanup()
		a.dbError(w, e)
		return
	}
	defer tx.Rollback(r.Context())
	if kind == "schedule" {
		if _, e = tx.Exec(r.Context(), "SELECT id FROM content WHERE id=$1 FOR UPDATE", cid); e != nil {
			cleanup()
			a.dbError(w, e)
			return
		}
		if _, e = tx.Exec(r.Context(), "DELETE FROM uploads WHERE content_id=$1", cid); e != nil {
			cleanup()
			a.dbError(w, e)
			return
		}
	}
	_, e = tx.Exec(r.Context(), "INSERT INTO uploads(id,organization_id,content_id,purpose,name,mime,path,thumbnail,size,image_width,image_height) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)", id, nullable(org), nullable(cid), purpose, name, mt, relative, thumb, len(b), imageWidth, imageHeight)
	if e != nil {
		cleanup()
		a.dbError(w, e)
		return
	}
	if cid != 0 {
		if _, e = tx.Exec(r.Context(), "UPDATE content SET updated_at=now() WHERE id=$1", cid); e != nil {
			cleanup()
			a.dbError(w, e)
			return
		}
	}
	if e = tx.Commit(r.Context()); e != nil {
		cleanup()
		a.dbError(w, e)
		return
	}
	write(w, 201, Upload{Width: imageWidth, Height: imageHeight, ID: id, Org: org, ContentID: cid, Purpose: purpose, Name: name, MIME: mt, Size: int64(len(b)), Caption: map[string]string{}})
}
func (a *App) file(w http.ResponseWriter, r *http.Request) {
	f, e := scanUpload(a.DB.QueryRow(r.Context(), "SELECT "+uploadColumns+" FROM uploads WHERE id=$1", r.PathValue("id")))
	if e != nil {
		a.dbError(w, e)
		return
	}
	st := state(r)
	var active, published bool
	if f.Org != 0 {
		if e = a.DB.QueryRow(r.Context(), "SELECT active FROM organizations WHERE id=$1", f.Org).Scan(&active); e != nil {
			a.dbError(w, e)
			return
		}
	} else {
		active = true
	}
	if f.Purpose == "logo" {
		var attached bool
		if e = a.DB.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM organizations WHERE id=$1 AND profile->>'logo'=$2)", f.Org, f.ID).Scan(&attached); e != nil {
			a.dbError(w, e)
			return
		}
		published = attached
	} else {
		if e = a.DB.QueryRow(r.Context(), "SELECT published FROM content WHERE id=$1", f.ContentID).Scan(&published); e != nil {
			a.dbError(w, e)
			return
		}
	}
	authorized := st.User != nil && !st.User.MustChange && (st.User.Org == 0 || st.User.Org == f.Org)
	if !authorized && (!active || !published || st.Org != f.Org) {
		fail(w, 404, "Файл недоступен")
		return
	}
	path := f.Path
	mt := f.MIME
	if r.URL.Query().Get("thumbnail") == "1" && f.Thumbnail != "" {
		path = f.Thumbnail
		if path != f.Path {
			mt = "image/png"
		}
	}
	file, e := os.Open(filepath.Join(a.Uploads, path))
	if e != nil {
		fail(w, 404, "Файл не найден")
		return
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil {
		fail(w, 500, "Ошибка файла")
		return
	}
	w.Header().Set("Content-Type", mt)
	disposition := "attachment"
	if (mt == "application/pdf" || strings.HasPrefix(mt, "image/")) && r.URL.Query().Get("download") != "1" {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": f.Name}))
	if published && active {
		w.Header().Set("Cache-Control", "public, max-age=60")
	}
	http.ServeContent(w, r, f.Name, info.ModTime(), file)
}
func (a *App) editUpload(w http.ResponseWriter, r *http.Request) {
	f, e := scanUpload(a.DB.QueryRow(r.Context(), "SELECT "+uploadColumns+" FROM uploads WHERE id=$1", r.PathValue("id")))
	if e != nil {
		a.dbError(w, e)
		return
	}
	if !a.allow(w, r, f.Org) {
		return
	}
	var in struct {
		Caption  map[string]string `json:"caption"`
		Position int               `json:"position"`
	}
	if !decode(w, r, &in) {
		return
	}
	for lang, text := range in.Caption {
		if (lang != "kk" && lang != "ru") || len(text) > 2000 {
			fields(w, map[string]string{"caption": "Только kk/ru, до 2000 байт"})
			return
		}
	}
	if in.Caption == nil {
		in.Caption = map[string]string{}
	}
	_, e = a.DB.Exec(r.Context(), "UPDATE uploads SET caption=$1,position=$2 WHERE id=$3", in.Caption, in.Position, f.ID)
	if e != nil {
		a.dbError(w, e)
		return
	}
	w.WriteHeader(204)
}
func (a *App) deleteUpload(w http.ResponseWriter, r *http.Request) {
	f, e := scanUpload(a.DB.QueryRow(r.Context(), "SELECT "+uploadColumns+" FROM uploads WHERE id=$1", r.PathValue("id")))
	if e != nil {
		a.dbError(w, e)
		return
	}
	if !a.allow(w, r, f.Org) {
		return
	}
	_, e = a.DB.Exec(r.Context(), "DELETE FROM uploads WHERE id=$1", f.ID)
	if e != nil {
		a.dbError(w, e)
		return
	}
	w.WriteHeader(204)
}

// shortcut: deleted/replaced files stay on disk for backup consistency; run scripts/prune-uploads.py after the 14-day retention window.
