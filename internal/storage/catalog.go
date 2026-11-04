package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// CatalogArtifact links an application to something stored in a registry: an
// OCI repository (optionally one tag), or a file or folder of a path-addressed
// registry. Visibility of the application follows access to these.
type CatalogArtifact struct {
	ID       int64  `json:"id,omitempty"`
	Registry string `json:"registry"`
	Ref      string `json:"ref"` // OCI repository, or artifact path / prefix
	Tag      string `json:"tag"` // OCI only; empty = recent tags
	Label    string `json:"label"`
	Position int    `json:"position"`
}

// CatalogScreenshot describes one screenshot; the image itself is fetched apart.
type CatalogScreenshot struct {
	ID       int64  `json:"id"`
	Caption  string `json:"caption"`
	Position int    `json:"position"`
}

// CatalogApp is one entry of the application catalog.
type CatalogApp struct {
	ID          int64               `json:"id"`
	Slug        string              `json:"slug"`
	Name        string              `json:"name"`
	Tagline     string              `json:"tagline"`
	Description string              `json:"description"`
	Icon        string              `json:"icon"` // URL or data URI; omitted in listings
	Category    string              `json:"category"`
	Homepage    string              `json:"homepage"`
	Featured    bool                `json:"featured"`
	Position    int                 `json:"position"`
	CreatedAt   int64               `json:"created_at"`
	UpdatedAt   int64               `json:"updated_at"`
	Artifacts   []CatalogArtifact   `json:"artifacts"`
	Screenshots []CatalogScreenshot `json:"screenshots"`
}

// CatalogStore persists the catalog. PostgresMetadataStore implements it.
type CatalogStore interface {
	// ListCatalogApps returns every app with its artifacts, featured first then
	// by position and name. withIcon false leaves the (possibly large) icon out.
	ListCatalogApps(withIcon bool) ([]CatalogApp, error)
	GetCatalogApp(slug string) (*CatalogApp, error)
	GetCatalogAppByID(id int64) (*CatalogApp, error)
	// UpsertCatalogApp creates (ID 0) or updates an app, replacing its artifacts.
	UpsertCatalogApp(a CatalogApp) (int64, error)
	DeleteCatalogApp(id int64) error

	// Screenshots are managed apart from the app so saving an entry does not
	// resend its images.
	AddCatalogScreenshot(appID int64, image, caption string) (int64, error)
	UpdateCatalogScreenshot(appID, id int64, caption string) error
	DeleteCatalogScreenshot(appID, id int64) error
	// ReorderCatalogScreenshots sets the order to ids (all of the app's screenshots).
	ReorderCatalogScreenshots(appID int64, ids []int64) error
	CatalogScreenshotImage(appID, id int64) (string, error)
}

const appCols = `id, slug, name, tagline, description, %s, category, homepage, featured, position, created_at, updated_at`

func scanApp(row pgx.Row) (*CatalogApp, error) {
	var a CatalogApp
	if err := row.Scan(&a.ID, &a.Slug, &a.Name, &a.Tagline, &a.Description, &a.Icon, &a.Category,
		&a.Homepage, &a.Featured, &a.Position, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	a.Artifacts = []CatalogArtifact{}
	a.Screenshots = []CatalogScreenshot{}
	return &a, nil
}

func (m *PostgresMetadataStore) loadArtifacts(ctx context.Context, apps []CatalogApp) error {
	if len(apps) == 0 {
		return nil
	}
	idx := map[int64]*CatalogApp{}
	ids := make([]int64, 0, len(apps))
	for i := range apps {
		idx[apps[i].ID] = &apps[i]
		ids = append(ids, apps[i].ID)
	}
	srows, err := m.pool.Query(ctx,
		`SELECT id, app_id, caption, position FROM catalog_screenshots WHERE app_id = ANY($1) ORDER BY position, id`, ids)
	if err != nil {
		return err
	}
	for srows.Next() {
		var sc CatalogScreenshot
		var appID int64
		if err := srows.Scan(&sc.ID, &appID, &sc.Caption, &sc.Position); err != nil {
			srows.Close()
			return err
		}
		if a := idx[appID]; a != nil {
			a.Screenshots = append(a.Screenshots, sc)
		}
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return err
	}
	rows, err := m.pool.Query(ctx,
		`SELECT id, app_id, registry, ref, tag, label, position FROM catalog_artifacts
		 WHERE app_id = ANY($1) ORDER BY position, id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ar CatalogArtifact
		var appID int64
		if err := rows.Scan(&ar.ID, &appID, &ar.Registry, &ar.Ref, &ar.Tag, &ar.Label, &ar.Position); err != nil {
			return err
		}
		if a := idx[appID]; a != nil {
			a.Artifacts = append(a.Artifacts, ar)
		}
	}
	return rows.Err()
}

func (m *PostgresMetadataStore) ListCatalogApps(withIcon bool) ([]CatalogApp, error) {
	ctx := context.Background()
	icon := "''"
	if withIcon {
		icon = "icon"
	}
	rows, err := m.pool.Query(ctx, `SELECT `+sprintfCols(icon)+` FROM catalog_apps ORDER BY featured DESC, position, lower(name)`)
	if err != nil {
		return nil, err
	}
	out := []CatalogApp{}
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, *a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, m.loadArtifacts(ctx, out)
}

func sprintfCols(icon string) string { return fmt.Sprintf(appCols, icon) }

func (m *PostgresMetadataStore) getApp(where string, arg any) (*CatalogApp, error) {
	ctx := context.Background()
	a, err := scanApp(m.pool.QueryRow(ctx, `SELECT `+sprintfCols("icon")+` FROM catalog_apps WHERE `+where, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	list := []CatalogApp{*a}
	if err := m.loadArtifacts(ctx, list); err != nil {
		return nil, err
	}
	return &list[0], nil
}

func (m *PostgresMetadataStore) GetCatalogApp(slug string) (*CatalogApp, error) {
	return m.getApp("slug = $1", slug)
}

func (m *PostgresMetadataStore) GetCatalogAppByID(id int64) (*CatalogApp, error) {
	return m.getApp("id = $1", id)
}

func (m *PostgresMetadataStore) UpsertCatalogApp(a CatalogApp) (int64, error) {
	ctx := context.Background()
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	now := m.now()
	id := a.ID
	if id == 0 {
		err = tx.QueryRow(ctx,
			`INSERT INTO catalog_apps(slug, name, tagline, description, icon, category, homepage, featured, position, created_at, updated_at)
			 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10) RETURNING id`,
			a.Slug, a.Name, a.Tagline, a.Description, a.Icon, a.Category, a.Homepage, a.Featured, a.Position, now).Scan(&id)
	} else {
		var n int64
		err = tx.QueryRow(ctx,
			`UPDATE catalog_apps SET slug=$2, name=$3, tagline=$4, description=$5, icon=$6, category=$7,
			   homepage=$8, featured=$9, position=$10, updated_at=$11 WHERE id=$1 RETURNING id`,
			id, a.Slug, a.Name, a.Tagline, a.Description, a.Icon, a.Category, a.Homepage, a.Featured, a.Position, now).Scan(&n)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
	}
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM catalog_artifacts WHERE app_id = $1`, id); err != nil {
		return 0, err
	}
	for i, ar := range a.Artifacts {
		if _, err := tx.Exec(ctx,
			`INSERT INTO catalog_artifacts(app_id, registry, ref, tag, label, position) VALUES($1,$2,$3,$4,$5,$6)`,
			id, ar.Registry, ar.Ref, ar.Tag, ar.Label, i); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit(ctx)
}

func (m *PostgresMetadataStore) DeleteCatalogApp(id int64) error {
	tag, err := m.pool.Exec(context.Background(), `DELETE FROM catalog_apps WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *PostgresMetadataStore) AddCatalogScreenshot(appID int64, image, caption string) (int64, error) {
	var id int64
	err := m.pool.QueryRow(context.Background(),
		`INSERT INTO catalog_screenshots(app_id, image, caption, position)
		 VALUES($1,$2,$3,(SELECT COALESCE(MAX(position)+1, 0) FROM catalog_screenshots WHERE app_id=$1)) RETURNING id`,
		appID, image, caption).Scan(&id)
	return id, err
}

func (m *PostgresMetadataStore) UpdateCatalogScreenshot(appID, id int64, caption string) error {
	tag, err := m.pool.Exec(context.Background(),
		`UPDATE catalog_screenshots SET caption=$3 WHERE app_id=$1 AND id=$2`, appID, id, caption)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *PostgresMetadataStore) DeleteCatalogScreenshot(appID, id int64) error {
	tag, err := m.pool.Exec(context.Background(),
		`DELETE FROM catalog_screenshots WHERE app_id=$1 AND id=$2`, appID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *PostgresMetadataStore) ReorderCatalogScreenshots(appID int64, ids []int64) error {
	ctx := context.Background()
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for i, id := range ids {
		if _, err := tx.Exec(ctx,
			`UPDATE catalog_screenshots SET position=$3 WHERE app_id=$1 AND id=$2`, appID, id, i); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (m *PostgresMetadataStore) CatalogScreenshotImage(appID, id int64) (string, error) {
	var img string
	err := m.pool.QueryRow(context.Background(),
		`SELECT image FROM catalog_screenshots WHERE app_id=$1 AND id=$2`, appID, id).Scan(&img)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return img, err
}
