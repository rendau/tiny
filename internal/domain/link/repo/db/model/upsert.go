package model

import (
	"time"

	domainModel "github.com/rendau/tiny/internal/domain/link/model"
)

type Upsert struct {
	PKId  int64
	NewId int64 // id новой записи, заполняется в Create через RETURNING

	CreatedAt    *time.Time
	ModifiedAt   *time.Time
	Domain       *string
	ShortCode    *string
	LongUrl      *string
	Title        *string
	Tags         *[]string
	ValidSince   **time.Time
	ValidUntil   **time.Time
	MaxVisits    **int32
	Crawlable    *bool
	ForwardQuery *bool
}

func (m *Upsert) CreateColumnMap() map[string]any {
	result := make(map[string]any, 12)
	if m.CreatedAt != nil {
		result["created_at"] = *m.CreatedAt
	}
	if m.ModifiedAt != nil {
		result["modified_at"] = *m.ModifiedAt
	}
	if m.Domain != nil {
		result["domain"] = *m.Domain
	}
	if m.ShortCode != nil {
		result["short_code"] = *m.ShortCode
	}
	if m.LongUrl != nil {
		result["long_url"] = *m.LongUrl
	}
	if m.Title != nil {
		result["title"] = *m.Title
	}
	if m.Tags != nil {
		// колонка not null: пустой набор — '{}', а не NULL
		tags := *m.Tags
		if tags == nil {
			tags = []string{}
		}
		result["tags"] = tags
	}
	// nullable-колонки: указатель на nil пишет NULL
	if m.ValidSince != nil {
		result["valid_since"] = *m.ValidSince
	}
	if m.ValidUntil != nil {
		result["valid_until"] = *m.ValidUntil
	}
	if m.MaxVisits != nil {
		result["max_visits"] = *m.MaxVisits
	}
	if m.Crawlable != nil {
		result["crawlable"] = *m.Crawlable
	}
	if m.ForwardQuery != nil {
		result["forward_query"] = *m.ForwardQuery
	}
	return result
}

func (m *Upsert) UpdateColumnMap() map[string]any {
	return m.CreateColumnMap()
}

func (m *Upsert) PKColumnMap() map[string]any {
	return map[string]any{"id": m.PKId}
}

// ReturningColumnMap — Create использует RETURNING id, чтобы вернуть id новой записи
func (m *Upsert) ReturningColumnMap() map[string]any {
	return map[string]any{"id": &m.NewId}
}

// DTO

func DecodeUpsert(v *domainModel.Edit) *Upsert {
	return &Upsert{
		CreatedAt:    v.CreatedAt,
		ModifiedAt:   v.ModifiedAt,
		Domain:       v.Domain,
		ShortCode:    v.ShortCode,
		LongUrl:      v.LongUrl,
		Title:        v.Title,
		Tags:         v.Tags,
		ValidSince:   v.ValidSince,
		ValidUntil:   v.ValidUntil,
		MaxVisits:    v.MaxVisits,
		Crawlable:    v.Crawlable,
		ForwardQuery: v.ForwardQuery,
	}
}
