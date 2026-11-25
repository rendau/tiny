package model

import (
	"time"

	commonModel "github.com/rendau/tiny/internal/domain/common/model"
)

// Main — короткая ссылка
type Main struct {
	Id         int64
	CreatedAt  time.Time
	ModifiedAt time.Time
	// Domain — пусто: ссылка основного домена (открывается на любом нашем домене)
	Domain    string
	ShortCode string
	LongUrl   string
	Title     string
	Tags      []string
	// ValidSince, ValidUntil — с какого и до какого времени ссылка работает; nil — без ограничения
	ValidSince *time.Time
	ValidUntil *time.Time
	// MaxVisits — хранится для совместимости с Shlink; посещения не считаются
	MaxVisits    *int32
	Crawlable    bool
	ForwardQuery bool

	// ShortUrl — полный короткий адрес (https://l.demo.test/aBc12). В базе не хранится:
	// собирает usecase из основного домена и кода
	ShortUrl string
}

// Edit — мутация: nil — поле не передано (при создании — значение по умолчанию из БД,
// при правке — не меняется)
type Edit struct {
	CreatedAt  *time.Time // обычно nil (DB-default); для переноса с исходной датой
	ModifiedAt *time.Time
	Domain     *string
	ShortCode  *string
	LongUrl    *string
	Title      *string
	Tags       *[]string
	// поля, которые бывают NULL: внешний указатель nil — не передано, указатель на nil — записать NULL
	// (снять ограничение)
	ValidSince   **time.Time
	ValidUntil   **time.Time
	MaxVisits    **int32
	Crawlable    *bool
	ForwardQuery *bool
}

// ListReq — параметры выборки
type ListReq struct {
	commonModel.ListParams

	// Search — подстрока в длинном адресе или названии
	Search    *string
	ShortCode *string
	// Tag — ссылки с этой меткой
	Tag *string
	// Expired — true: только истёкшие (valid_until в прошлом), false: только действующие
	Expired *bool
	Domain  *string
}

// FindExistingReq — поиск уже существующей ссылки (findIfExists Shlink): сравнивается длинный адрес
// и только те параметры, которые переданы (nil — не сравнивается). Название, crawlable и
// forward_query не сравниваются — как в Shlink
type FindExistingReq struct {
	LongUrl    string
	ShortCode  *string
	Domain     *string
	ValidSince *time.Time
	ValidUntil *time.Time
	MaxVisits  *int32
	// Tags — пусто: метки не сравниваются; иначе у ссылки должен быть ровно этот набор
	Tags []string
}

// CreateReq — запрос на создание ссылки (админка и POST /rest/v3/short-urls)
type CreateReq struct {
	LongUrl string
	// ShortCode — свой код; nil или пусто — сгенерировать
	ShortCode *string
	// ShortCodeLength — длина генерируемого кода; nil — длина по умолчанию
	ShortCodeLength *int
	// Domain — nil или основной домен: ссылка основного домена
	Domain       *string
	Title        *string
	Tags         []string
	ValidSince   *time.Time
	ValidUntil   *time.Time
	MaxVisits    *int32
	Crawlable    *bool // nil — false
	ForwardQuery *bool // nil — true
	// FindIfExists — вернуть уже существующую ссылку с такими параметрами, новую не создавать
	FindIfExists bool
}
