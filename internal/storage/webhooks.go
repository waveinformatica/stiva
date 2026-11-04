package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Webhook is one outgoing notification target.
type Webhook struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	URL         string   `json:"url"`
	Secret      string   `json:"secret"`     // vault reference ("" = deliveries are unsigned)
	Events      []string `json:"events"`     // event names or patterns ("push.*", "*")
	Registries  []string `json:"registries"` // empty = every registry
	Enabled     bool     `json:"enabled"`
	Description string   `json:"description"`
	CreatedAt   int64    `json:"created_at"`
}

// WebhookDelivery is one attempt series to deliver one event to one webhook.
type WebhookDelivery struct {
	ID            int64           `json:"id"`
	WebhookID     int64           `json:"webhook_id"`
	Event         string          `json:"event"`
	Registry      string          `json:"registry"`
	Payload       json.RawMessage `json:"payload,omitempty"`
	Status        string          `json:"status"`
	Attempts      int             `json:"attempts"`
	NextAttemptAt int64           `json:"next_attempt_at"`
	StatusCode    int             `json:"status_code"`
	Error         string          `json:"error,omitempty"`
	CreatedAt     int64           `json:"created_at"`
	DeliveredAt   int64           `json:"delivered_at"`
}

// WebhookStore persists webhooks and the delivery queue. PostgresMetadataStore implements it.
type WebhookStore interface {
	ListWebhooks() ([]Webhook, error)
	GetWebhook(id int64) (*Webhook, error)
	UpsertWebhook(w Webhook) (int64, error)
	DeleteWebhook(id int64) error
	// WebhooksUsingSecret lists the names of webhooks referencing a vault key.
	WebhooksUsingSecret(ref string) ([]string, error)

	EnqueueDelivery(webhookID int64, event, registry string, payload []byte, at time.Time) (int64, error)
	// ClaimDelivery takes the oldest due delivery and marks it in flight.
	ClaimDelivery(now time.Time) (*WebhookDelivery, error)
	// FinishDelivery records an attempt. retryAt zero means the delivery is over.
	FinishDelivery(id int64, ok bool, statusCode int, errMsg string, retryAt time.Time) error
	// RecoverDeliveries returns deliveries left in flight by a dead instance to the queue.
	RecoverDeliveries(olderThan time.Duration) (int64, error)
	GetDelivery(id int64) (*WebhookDelivery, error)
	ListDeliveries(webhookID int64, limit int) ([]WebhookDelivery, error)
	// RetryDelivery puts a finished delivery back in the queue.
	RetryDelivery(id int64, at time.Time) error
	PruneDeliveries(olderThan time.Time) (int64, error)
}

const whCols = `id, name, url, secret, events, registries, enabled, description, created_at`

func scanWebhook(row pgx.Row) (*Webhook, error) {
	var w Webhook
	if err := row.Scan(&w.ID, &w.Name, &w.URL, &w.Secret, &w.Events, &w.Registries, &w.Enabled, &w.Description, &w.CreatedAt); err != nil {
		return nil, err
	}
	if w.Events == nil {
		w.Events = []string{}
	}
	if w.Registries == nil {
		w.Registries = []string{}
	}
	return &w, nil
}

func (m *PostgresMetadataStore) ListWebhooks() ([]Webhook, error) {
	rows, err := m.pool.Query(context.Background(), `SELECT `+whCols+` FROM webhooks ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Webhook{}
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

func (m *PostgresMetadataStore) GetWebhook(id int64) (*Webhook, error) {
	w, err := scanWebhook(m.pool.QueryRow(context.Background(), `SELECT `+whCols+` FROM webhooks WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return w, err
}

func (m *PostgresMetadataStore) UpsertWebhook(w Webhook) (int64, error) {
	if w.Events == nil {
		w.Events = []string{}
	}
	if w.Registries == nil {
		w.Registries = []string{}
	}
	ctx := context.Background()
	if w.ID == 0 {
		var id int64
		err := m.pool.QueryRow(ctx,
			`INSERT INTO webhooks(name, url, secret, events, registries, enabled, description, created_at)
			 VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
			w.Name, w.URL, w.Secret, w.Events, w.Registries, w.Enabled, w.Description, m.now()).Scan(&id)
		return id, err
	}
	tag, err := m.pool.Exec(ctx,
		`UPDATE webhooks SET name=$2, url=$3, secret=$4, events=$5, registries=$6, enabled=$7, description=$8 WHERE id=$1`,
		w.ID, w.Name, w.URL, w.Secret, w.Events, w.Registries, w.Enabled, w.Description)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		return 0, ErrNotFound
	}
	return w.ID, nil
}

func (m *PostgresMetadataStore) DeleteWebhook(id int64) error {
	tag, err := m.pool.Exec(context.Background(), `DELETE FROM webhooks WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *PostgresMetadataStore) WebhooksUsingSecret(ref string) ([]string, error) {
	rows, err := m.pool.Query(context.Background(), `SELECT name FROM webhooks WHERE secret = $1 ORDER BY name`, ref)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (m *PostgresMetadataStore) EnqueueDelivery(webhookID int64, event, registry string, payload []byte, at time.Time) (int64, error) {
	var id int64
	err := m.pool.QueryRow(context.Background(),
		`INSERT INTO webhook_deliveries(webhook_id, event, registry, payload, next_attempt_at, created_at)
		 VALUES($1,$2,$3,$4,$5,$6) RETURNING id`,
		webhookID, event, registry, payload, at.UnixNano(), m.now()).Scan(&id)
	return id, err
}

const wdCols = `id, webhook_id, event, registry, payload, status, attempts, next_attempt_at, status_code, error, created_at, delivered_at`

func scanDelivery(row pgx.Row) (*WebhookDelivery, error) {
	var d WebhookDelivery
	var payload []byte
	if err := row.Scan(&d.ID, &d.WebhookID, &d.Event, &d.Registry, &payload, &d.Status, &d.Attempts,
		&d.NextAttemptAt, &d.StatusCode, &d.Error, &d.CreatedAt, &d.DeliveredAt); err != nil {
		return nil, err
	}
	d.Payload = payload
	return &d, nil
}

func (m *PostgresMetadataStore) ClaimDelivery(now time.Time) (*WebhookDelivery, error) {
	d, err := scanDelivery(m.pool.QueryRow(context.Background(),
		`UPDATE webhook_deliveries SET status = 'delivering', attempts = attempts + 1, next_attempt_at = $1
		 WHERE id = (SELECT id FROM webhook_deliveries WHERE status = 'pending' AND next_attempt_at <= $1
		             ORDER BY next_attempt_at, id FOR UPDATE SKIP LOCKED LIMIT 1)
		 RETURNING `+wdCols, now.UnixNano()))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return d, err
}

func (m *PostgresMetadataStore) FinishDelivery(id int64, ok bool, statusCode int, errMsg string, retryAt time.Time) error {
	ctx := context.Background()
	switch {
	case ok:
		_, err := m.pool.Exec(ctx,
			`UPDATE webhook_deliveries SET status='succeeded', status_code=$2, error='', delivered_at=$3 WHERE id=$1`,
			id, statusCode, m.now())
		return err
	case !retryAt.IsZero():
		_, err := m.pool.Exec(ctx,
			`UPDATE webhook_deliveries SET status='pending', status_code=$2, error=$3, next_attempt_at=$4 WHERE id=$1`,
			id, statusCode, errMsg, retryAt.UnixNano())
		return err
	default:
		_, err := m.pool.Exec(ctx,
			`UPDATE webhook_deliveries SET status='failed', status_code=$2, error=$3, delivered_at=$4 WHERE id=$1`,
			id, statusCode, errMsg, m.now())
		return err
	}
}

func (m *PostgresMetadataStore) RecoverDeliveries(olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan).UnixNano()
	tag, err := m.pool.Exec(context.Background(),
		`UPDATE webhook_deliveries SET status='pending', next_attempt_at=$2
		 WHERE status='delivering' AND next_attempt_at < $1`, cutoff, m.now())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (m *PostgresMetadataStore) GetDelivery(id int64) (*WebhookDelivery, error) {
	d, err := scanDelivery(m.pool.QueryRow(context.Background(),
		`SELECT `+wdCols+` FROM webhook_deliveries WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func (m *PostgresMetadataStore) ListDeliveries(webhookID int64, limit int) ([]WebhookDelivery, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := m.pool.Query(context.Background(),
		`SELECT `+wdCols+` FROM webhook_deliveries WHERE webhook_id = $1 ORDER BY id DESC LIMIT $2`, webhookID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WebhookDelivery{}
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		d.Payload = nil // the list never needs bodies
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (m *PostgresMetadataStore) RetryDelivery(id int64, at time.Time) error {
	tag, err := m.pool.Exec(context.Background(),
		`UPDATE webhook_deliveries SET status='pending', attempts=0, error='', next_attempt_at=$2
		 WHERE id=$1 AND status IN ('succeeded','failed')`, id, at.UnixNano())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (m *PostgresMetadataStore) PruneDeliveries(olderThan time.Time) (int64, error) {
	tag, err := m.pool.Exec(context.Background(),
		`DELETE FROM webhook_deliveries WHERE status IN ('succeeded','failed') AND created_at < $1`, olderThan.UnixNano())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
