package model

import (
	"time"

	domainModel "github.com/rendau/tiny/internal/domain/link/model"
)

// SelectColumns — колонки чтения в порядке полей Select.ScanDest (для кастомных запросов)
const SelectColumns = "id, created_at, modified_at, domain, short_code, long_url, title, tags, " +
	"valid_since, valid_until, max_visits, forward_query"

type Select struct {
	Id           int64
	CreatedAt    time.Time
	ModifiedAt   time.Time
	Domain       string
	ShortCode    string
	LongUrl      string
	Title        string
	Tags         []string
	ValidSince   *time.Time
	ValidUntil   *time.Time
	MaxVisits    *int32
	ForwardQuery bool
}

func (m *Select) ListColumnMap() map[string]any {
	return map[string]any{
		"id":            &m.Id,
		"created_at":    &m.CreatedAt,
		"modified_at":   &m.ModifiedAt,
		"domain":        &m.Domain,
		"short_code":    &m.ShortCode,
		"long_url":      &m.LongUrl,
		"title":         &m.Title,
		"tags":          &m.Tags,
		"valid_since":   &m.ValidSince,
		"valid_until":   &m.ValidUntil,
		"max_visits":    &m.MaxVisits,
		"forward_query": &m.ForwardQuery,
	}
}

// ScanDest — указатели на поля в порядке SelectColumns
func (m *Select) ScanDest() []any {
	return []any{
		&m.Id, &m.CreatedAt, &m.ModifiedAt, &m.Domain, &m.ShortCode, &m.LongUrl, &m.Title, &m.Tags,
		&m.ValidSince, &m.ValidUntil, &m.MaxVisits, &m.ForwardQuery,
	}
}

func (m *Select) PKColumnMap() map[string]any {
	return map[string]any{"id": m.Id}
}

// DefaultSortColumns — новые первыми; совпадает с индексом link_created_at_idx
func (m *Select) DefaultSortColumns() []string {
	return []string{"created_at DESC", "id DESC"}
}

// DTO

func EncodeSelect(v *Select, _ int) *domainModel.Main {
	tags := v.Tags
	if tags == nil {
		tags = []string{}
	}

	return &domainModel.Main{
		Id:           v.Id,
		CreatedAt:    v.CreatedAt,
		ModifiedAt:   v.ModifiedAt,
		Domain:       v.Domain,
		ShortCode:    v.ShortCode,
		LongUrl:      v.LongUrl,
		Title:        v.Title,
		Tags:         tags,
		ValidSince:   v.ValidSince,
		ValidUntil:   v.ValidUntil,
		MaxVisits:    v.MaxVisits,
		ForwardQuery: v.ForwardQuery,
	}
}
