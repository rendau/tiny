package shlinkimport

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

// TestSeedStand наполняет настоящий Shlink стенда разными ссылками через его же API.
// Запускается только явно: SHLINK_IMPORT_SEED=6000 go test ./internal/shlinkimport/ -run TestSeedStand -v
// Повторный запуск добавляет ещё столько же (адреса уникальны за счёт метки запуска).
func TestSeedStand(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("SHLINK_IMPORT_SEED"))
	if n <= 0 {
		t.Skip("наполнение стенда запускается явно: SHLINK_IMPORT_SEED=<сколько ссылок>")
	}
	shlinkOrSkip(t)

	run := randSuffix(t)
	var created, failed atomic.Int64

	// Сначала по одной: при 16 потоках, одновременно создававших ссылки с ещё не существующими
	// метками, Shlink 5.1.8 намертво завис (обработчики остались ждать с открытой транзакцией,
	// помог только перезапуск). Поэтому все метки и второй домен заводим заранее, по очереди,
	// и только потом идём в несколько потоков.
	warmCtx := context.Background()
	for i := range min(n, 240) {
		_, err := shlinkCreate(warmCtx, seedBody(run, i))
		require.NoError(t, err, "разогрев, ссылка %d", i)
		created.Add(1)
	}

	workers, _ := strconv.Atoi(os.Getenv("SHLINK_IMPORT_SEED_WORKERS"))
	if workers <= 0 {
		workers = 4
	}

	g, ctx := errgroup.WithContext(context.Background())
	g.SetLimit(workers)
	for i := 240; i < n; i++ {
		g.Go(func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := shlinkCreate(ctx, seedBody(run, i)); err != nil {
				// занятый случайный код и подобное — не повод останавливать наполнение
				if failed.Add(1) > int64(n/20+10) {
					return fmt.Errorf("слишком много отказов Shlink, последний: %w", err)
				}
				return nil
			}
			created.Add(1)
			return nil
		})
	}
	require.NoError(t, g.Wait())
	t.Logf("создано %d ссылок, отказов %d", created.Load(), failed.Load())
}

// seedBody — тело запроса создания ссылки номер i: по номеру подмешиваются метки, сроки,
// предел посещений, название, свой код, второй домен, crawlable, forwardQuery=false,
// очень длинные адреса, пробелы и кириллица, коды длиной 4, 5 и 6.
func seedBody(run string, i int) map[string]any {
	now := time.Now()
	body := map[string]any{
		"longUrl":         fmt.Sprintf("https://www.demo.test/product/%d?run=%s&utm_source=sms", i, run),
		"shortCodeLength": 4 + i%3,
	}

	switch {
	case i%50 == 7: // очень длинный адрес (десятки килобайт)
		body["longUrl"] = fmt.Sprintf("https://www.demo.test/cart/%d?run=%s&items=%s",
			i, run, strings.Repeat("a1b2c3d4e5,", 500+i%3000))
	case i%37 == 5: // пробелы и кириллица как есть
		body["longUrl"] = fmt.Sprintf("https://www.demo.test/поиск/%d?q=холодильник двухкамерный&город=Алматы&run=%s", i, run)
	case i%41 == 3: // кодированные символы, якорь, амперсанды
		body["longUrl"] = fmt.Sprintf("https://www.demo.test/p/%d?q=%%D1%%82%%D0%%B2&a=1&b=2&run=%s#отзывы", i, run)
	}

	switch i % 6 {
	case 1:
		body["tags"] = []string{"mail_sms"}
	case 2:
		body["tags"] = []string{"delivery", "courier"}
	case 3:
		body["tags"] = []string{"Zeta", "акция", "x1", "Tag With Space"}
	case 4:
		body["tags"] = []string{"shop", fmt.Sprintf("магазин-%d", i%40)}
	}

	if i%11 == 0 { // срок «с»: то прошедший, то будущий, в разных записях времени
		since := now.Add(time.Duration(i%7-3) * 24 * time.Hour)
		body["validSince"] = since.UTC().Format(time.RFC3339)
	}
	if i%5 == 0 { // срок «до»: то истёкший, то будущий
		until := now.Add(time.Duration(i%9-2) * 24 * time.Hour)
		if i%2 == 0 {
			body["validUntil"] = until.In(time.FixedZone("", 5*3600)).Format("2006-01-02T15:04:05-07:00")
		} else {
			body["validUntil"] = until.In(time.FixedZone("", 3*3600)).Format(time.RFC3339Nano)
		}
	}
	if i%13 == 0 {
		body["maxVisits"] = 1 + i%1000
	}
	switch {
	case i%70 == 0:
		body["title"] = string([]rune(strings.Repeat("Длинное название ", 30))[:500])
	case i%7 == 0:
		body["title"] = fmt.Sprintf("Товар №%d — «скидка» 50%% & подарок", i)
	case i%29 == 0:
		body["title"] = ""
	}
	if i%17 == 0 { // свой код: латиница разного регистра, кириллица
		if i%34 == 0 {
			body["customSlug"] = fmt.Sprintf("Promo-%s-%d", run, i)
		} else {
			body["customSlug"] = fmt.Sprintf("акция-%s-%d", run, i)
		}
		delete(body, "shortCodeLength")
	}
	if i%9 == 0 {
		body["domain"] = "sl.demo.test"
	}
	if i%19 == 0 {
		body["crawlable"] = true
	}
	if i%23 == 0 {
		body["forwardQuery"] = false
	}
	if i%101 == 0 { // один и тот же свой код на основном и на втором домене — это разные ссылки
		body["customSlug"] = fmt.Sprintf("same-%s-%d", run, i/101)
		delete(body, "shortCodeLength")
		if i%202 == 0 {
			body["domain"] = "sl.demo.test"
		} else {
			delete(body, "domain")
		}
	}
	return body
}
