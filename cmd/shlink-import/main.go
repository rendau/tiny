// shlink-import — разовый перенос коротких ссылок из базы Shlink 5.1.8 в базу tiny.
// Как запускать и порядок действий при переключении — в README.md рядом.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // свои данные о часовых поясах: в контейнере системных может не быть

	"github.com/rendau/tiny/internal/shlinkimport"
)

const usage = `shlink-import — перенос коротких ссылок из базы Shlink 5.1.8 в базу tiny.

Адреса баз берутся только из переменных окружения и нигде не печатаются:
  SRC_DSN  база Shlink (из неё только читаем)   postgres://логин:пароль@хост:порт/база
  DST_DSN  база tiny (таблица link)             postgres://логин:пароль@хост:порт/база
           не задан — берётся PG_DSN (адрес базы самого сервиса: так инструмент запускают в его поде)
  SRC_TZ   пояс, в котором работает Shlink (его настройка TIMEZONE), например Asia/Almaty;
           можно задать и флагом -src-tz

Режимы:
  (без флагов)   перенести все ссылки; повторный запуск безопасен
  -new           докачать только то, что появилось после последней перенесённой ссылки
  -since МОМЕНТ  докачать только созданное не раньше момента (2026-11-25T10:00:00+05:00)
  -after-id N    докачать только ссылки с id источника больше N
  -dry-run       пробный запуск без записи (сочетается с режимами выше)
  -verify        проверка после переноса: число ссылок и сверка случайной выборки по всем полям
  -survey        только обзор источника: что в нём есть и что не переносится

Флаги:
`

func main() {
	os.Exit(run())
}

func run() int {
	var (
		dryRun       = flag.Bool("dry-run", false, "пробный запуск: в приёмник ничего не пишется")
		verify       = flag.Bool("verify", false, "проверка после переноса (ничего не пишет)")
		verifySample = flag.Int("verify-sample", shlinkimport.DefaultVerifySample, "сколько случайных ссылок сверять при -verify")
		verifyAll    = flag.Bool("verify-all", false, "при -verify сверять все ссылки, а не выборку")
		survey       = flag.Bool("survey", false, "только показать обзор источника")
		onlyNew      = flag.Bool("new", false, "докачать только то, что появилось после последней перенесённой ссылки")
		overlap      = flag.Int64("overlap", shlinkimport.DefaultOverlap, "при -new: на сколько id отступить назад от последней перенесённой (запас; повтор безвреден)")
		sinceStr     = flag.String("since", "", "докачать только ссылки, созданные не раньше момента (RFC3339, например 2026-11-25T10:00:00+05:00)")
		afterID      = flag.Int64("after-id", 0, "докачать только ссылки с id источника больше указанного")
		batch        = flag.Int("batch", shlinkimport.DefaultBatchSize, "ссылок в одной пачке")
		srcTZ        = flag.String("src-tz", os.Getenv("SRC_TZ"), "пояс, в котором работает Shlink (по умолчанию — из SRC_TZ)")
		progress     = flag.Duration("progress", shlinkimport.DefaultProgressEvery, "как часто писать ход")
	)
	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), usage)
		flag.PrintDefaults()
	}
	flag.Parse()

	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stdout, time.Now().Format("15:04:05")+" "+format+"\n", args...)
	}
	fail := func(format string, args ...any) int {
		fmt.Fprintf(os.Stderr, "ОШИБКА: "+format+"\n", args...)
		return 1
	}

	if flag.NArg() > 0 {
		fail("лишние аргументы: %v (подсказка: -h)", flag.Args())
		return 2
	}

	srcDSN, dstDSN := os.Getenv("SRC_DSN"), os.Getenv("DST_DSN")
	if dstDSN == "" {
		// в поде сервиса адрес его базы уже есть
		dstDSN = os.Getenv("PG_DSN")
	}
	if srcDSN == "" || (dstDSN == "" && !*survey) {
		fail("задайте адреса баз в переменных окружения SRC_DSN и DST_DSN (подсказка: -h)")
		return 2
	}
	if *srcTZ == "" {
		fail("задайте пояс, в котором работает Shlink: переменная SRC_TZ или флаг -src-tz (например Asia/Almaty; как узнать — см. README.md)")
		return 2
	}
	loc, err := time.LoadLocation(*srcTZ)
	if err != nil {
		fail("неизвестный часовой пояс %q: ожидается название вида Asia/Almaty или UTC", *srcTZ)
		return 2
	}

	var since *time.Time
	if *sinceStr != "" {
		t, err := time.Parse(time.RFC3339, *sinceStr)
		if err != nil {
			fail("момент в -since не разобран: ожидается вид 2026-11-25T10:00:00+05:00")
			return 2
		}
		since = &t
	}

	modes := 0
	for _, on := range []bool{*verify, *survey} {
		if on {
			modes++
		}
	}
	if modes > 1 || (modes == 1 && (*dryRun || *onlyNew || since != nil || *afterID > 0)) {
		fail("-verify и -survey — отдельные режимы, с другими режимами не сочетаются")
		return 2
	}
	if *verifyAll && !*verify {
		fail("-verify-all работает только вместе с -verify")
		return 2
	}

	// Ctrl+C и SIGTERM: останавливаемся после текущей пачки; записанное остаётся, повтор безопасен
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch {
	case *survey:
		err = shlinkimport.PrintSurvey(ctx, srcDSN, loc, logf)
		if err != nil {
			return fail("%v", err)
		}
		return 0

	case *verify:
		res, err := shlinkimport.Verify(ctx, shlinkimport.VerifyOptions{
			SrcDSN:        srcDSN,
			DstDSN:        dstDSN,
			SrcLocation:   loc,
			Sample:        *verifySample,
			All:           *verifyAll,
			BatchSize:     *batch,
			ProgressEvery: *progress,
			Logf:          logf,
		})
		if err != nil {
			return fail("%v", err)
		}
		if !res.OK {
			return 1
		}
		return 0

	default:
		_, err = shlinkimport.Import(ctx, shlinkimport.ImportOptions{
			SrcDSN:        srcDSN,
			DstDSN:        dstDSN,
			SrcLocation:   loc,
			BatchSize:     *batch,
			AfterID:       *afterID,
			Since:         since,
			New:           *onlyNew,
			Overlap:       *overlap,
			DryRun:        *dryRun,
			Survey:        !*onlyNew && since == nil && *afterID == 0, // обзор — только при полном переносе
			ProgressEvery: *progress,
			Logf:          logf,
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return fail("остановлено по сигналу. %v", err)
			}
			return fail("%v", err)
		}
		return 0
	}
}
