package config

import (
	"github.com/caarlos0/env/v9"
	_ "github.com/joho/godotenv/autoload"
)

var Conf = struct {
	Debug    bool   `env:"DEBUG" envDefault:"false"`
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`

	WithTracing   bool   `env:"WITH_TRACING" envDefault:"false"`
	JaegerAddress string `env:"JAEGER_ADDRESS"`

	// API админки: gRPC и HTTP-шлюз к нему
	GrpcPort string `env:"GRPC_PORT" envDefault:"5050"`
	HttpPort string `env:"HTTP_PORT" envDefault:"80"`
	HttpCors bool   `env:"HTTP_CORS" envDefault:"false"`

	PgDsn string `env:"PG_DSN"`

	// подпись токенов входа в админку (HS256)
	JwtSecret string `env:"JWT_SECRET"`

	// публичный сервер, совместимый с Shlink: переход по короткой ссылке (/{код}) и /rest/v3/*.
	// Порт по умолчанию тот же, что слушал контейнер Shlink
	PublicPort string `env:"PUBLIC_PORT" envDefault:"8080"`
	// ключи для /rest/* (заголовок X-Api-Key), через запятую
	ApiKeys []string `env:"API_KEYS" envSeparator:","`
	// те же ключи, заданные хешем (SHA-256 в hex — так Shlink хранит их в api_keys.key), через
	// запятую: ключи переезжают из Shlink без переноса самих значений. Оба списка складываются;
	// оба пусты — /rest/* отвечает 401 всем
	ApiKeyHashes []string `env:"API_KEY_HASHES" envSeparator:","`
	// писать в журнал каждый запрос создания ссылки вместе с телом: чтобы после переключения с
	// Shlink увидеть, что присылают клиенты. В длинных адресах могут быть личные данные — включать на время.
	// Отказы на /rest/* пишутся всегда, без тела запроса
	LogCreateRequests bool `env:"LOG_CREATE_REQUESTS" envDefault:"false"`
	// основной домен: с ним собирается shortUrl в ответах (имена переменных — как у Shlink)
	DefaultDomain  string `env:"DEFAULT_DOMAIN" envDefault:"localhost:8080"`
	IsHttpsEnabled bool   `env:"IS_HTTPS_ENABLED" envDefault:"true"`
	// длина кода, если в запросе не указана
	DefaultShortCodesLength int `env:"DEFAULT_SHORT_CODES_LENGTH" envDefault:"5"`

	// интерфейс админки (SPA): статика из AdminDir на своём порту; адрес API — в /config.js
	AdminPort       string `env:"ADMIN_PORT" envDefault:"8001"`
	AdminDir        string `env:"ADMIN_DIR" envDefault:"./admin-ui"`
	AdminApiBaseUrl string `env:"ADMIN_API_BASE_URL"`
}{}

func init() {
	if err := env.Parse(&Conf); err != nil {
		panic(err)
	}
}
