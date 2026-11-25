package shlinkimport

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Logf — куда писать журнал хода.
type Logf func(format string, args ...any)

const (
	DefaultBatchSize     = 5000
	DefaultOverlap       = 10000
	DefaultProgressEvery = 5 * time.Second

	maxReportedKeys = 20
)

// ImportOptions — настройки переноса.
type ImportOptions struct {
	SrcDSN string // база Shlink; только читается
	DstDSN string // база tiny

	// SrcLocation — пояс, в котором Shlink хранит время (его настройка TIMEZONE). Обязателен.
	SrcLocation *time.Location

	BatchSize int // ссылок в пачке; 0 — DefaultBatchSize

	// Докачка — не больше одного из трёх:
	AfterID int64      // только ссылки с id источника больше указанного
	Since   *time.Time // только ссылки, созданные не раньше указанного момента
	New     bool       // только то, что появилось после последней перенесённой ссылки

	// Overlap — на сколько id отступить назад от последней перенесённой при New (запас на ссылки,
	// записанные в Shlink не по порядку id). Повтор безвреден. 0 — DefaultOverlap.
	Overlap int64

	DryRun bool // пробный режим: в приёмник ничего не пишется
	Survey bool // перед переносом показать обзор источника

	ProgressEvery time.Duration // как часто писать ход; 0 — DefaultProgressEvery
	Logf          Logf
}

// ImportResult — итог переноса.
type ImportResult struct {
	DryRun bool

	Total     int64 // ссылок источника, попавших в отбор
	Read      int64 // прочитано
	Inserted  int64 // записано новых (в пробном режиме — было бы записано)
	Existing  int64 // пропущено: уже есть в приёмнике
	Conflicts int64 // пропущено: пара «код + домен» занята ссылкой с другим длинным адресом

	ConflictKeys []Key // первые конфликты (не больше maxReportedKeys)

	FromID int64 // перенос шёл по id источника из (FromID, MaxID]
	MaxID  int64
	LastID int64 // id последней обработанной ссылки

	Elapsed time.Duration
}

func (o *ImportOptions) normalize() error {
	if o.SrcLocation == nil {
		return errors.New("не задан часовой пояс источника (в каком поясе работает Shlink)")
	}
	if o.BatchSize < 0 || o.AfterID < 0 || o.Overlap < 0 {
		return errors.New("размер пачки, id и запас не могут быть отрицательными")
	}
	modes := 0
	if o.AfterID > 0 {
		modes++
	}
	if o.Since != nil {
		modes++
	}
	if o.New {
		modes++
	}
	if modes > 1 {
		return errors.New("докачка задаётся одним способом: либо после id, либо с момента, либо «только новое»")
	}
	if o.BatchSize == 0 {
		o.BatchSize = DefaultBatchSize
	}
	if o.Overlap == 0 {
		o.Overlap = DefaultOverlap
	}
	if o.ProgressEvery == 0 {
		o.ProgressEvery = DefaultProgressEvery
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	return nil
}

// Import переносит ссылки из базы Shlink в таблицу link. Повторный запуск безопасен: ключ —
// код + домен, уже перенесённое не дублируется и не меняется.
func Import(ctx context.Context, opts ImportOptions) (*ImportResult, error) {
	if err := opts.normalize(); err != nil {
		return nil, err
	}
	res, err := doImport(ctx, opts)
	return res, safeError(err)
}

func doImport(ctx context.Context, opts ImportOptions) (*ImportResult, error) {
	logf := opts.Logf
	started := time.Now()

	src, err := openSource(ctx, opts.SrcDSN, opts.SrcLocation)
	if err != nil {
		return nil, err
	}
	defer src.close(ctx)

	dst, err := openDest(ctx, opts.DstDSN, opts.DryRun)
	if err != nil {
		return nil, err
	}
	defer dst.close(ctx)

	if opts.DryRun {
		logf("ПРОБНЫЙ РЕЖИМ: в приёмник ничего не пишется")
	}
	logf("время в источнике читается в поясе %s", opts.SrcLocation)

	if opts.Survey {
		survey, err := src.survey(ctx)
		if err != nil {
			return nil, err
		}
		survey.log(logf)
	}

	res := &ImportResult{DryRun: opts.DryRun}

	// граница переноса: всё, что появится в Shlink после этой точки, заберёт докачка
	res.MaxID, err = src.maxID(ctx)
	if err != nil {
		return nil, err
	}

	sc := scope{afterID: opts.AfterID, maxID: res.MaxID, since: opts.Since}
	switch {
	case opts.New:
		last, err := findLastImported(ctx, src, dst, res.MaxID, opts.BatchSize)
		if err != nil {
			return nil, err
		}
		sc.afterID = max(0, last-opts.Overlap)
		if last == 0 {
			logf("докачка: перенесённых ссылок в приёмнике не найдено — переносится всё")
		} else {
			logf("докачка: последняя перенесённая ссылка — id %d в источнике; с запасом в %d берём всё после id %d",
				last, opts.Overlap, sc.afterID)
		}
	case opts.Since != nil:
		logf("докачка: только ссылки, созданные не раньше %s", opts.Since.In(opts.SrcLocation).Format(time.RFC3339))
	case opts.AfterID > 0:
		logf("докачка: только ссылки с id источника больше %d", opts.AfterID)
	}
	res.FromID = sc.afterID
	res.LastID = sc.afterID

	res.Total, err = src.count(ctx, sc)
	if err != nil {
		return nil, err
	}
	logf("к переносу: %d ссылок (id источника от %d до %d), пачками по %d", res.Total, sc.afterID+1, res.MaxID, opts.BatchSize)

	var (
		buf          = make([]srcRow, 0, opts.BatchSize)
		lastProgress = time.Now()
	)
	for {
		if err = ctx.Err(); err != nil {
			return res, interrupted(res, err)
		}

		buf, err = src.readBatch(ctx, sc, res.LastID, opts.BatchSize, false, buf)
		if err != nil {
			return res, interrupted(res, err)
		}
		if len(buf) == 0 {
			break
		}

		var out *batchOutcome
		if opts.DryRun {
			out, err = dst.classifyBatch(ctx, buf)
		} else {
			out, err = dst.writeBatch(ctx, buf)
		}
		if err != nil {
			return res, interrupted(res, err)
		}

		res.Read += int64(len(buf))
		res.Inserted += out.Inserted
		res.Existing += out.Existing
		res.Conflicts += int64(len(out.Conflicts))
		if room := maxReportedKeys - len(res.ConflictKeys); room > 0 {
			res.ConflictKeys = append(res.ConflictKeys, out.Conflicts[:min(room, len(out.Conflicts))]...)
		}
		res.LastID = buf[len(buf)-1].SrcID

		if time.Since(lastProgress) >= opts.ProgressEvery {
			lastProgress = time.Now()
			res.logProgress(logf, time.Since(started))
		}

		if len(buf) < opts.BatchSize {
			break
		}
	}

	res.Elapsed = time.Since(started)
	res.logSummary(logf)
	return res, nil
}

// interrupted дополняет ошибку подсказкой, с какого места продолжать.
func interrupted(res *ImportResult, err error) error {
	return fmt.Errorf("перенос прерван после id источника %d (всё до него обработано; запустите ещё раз — повтор безопасен, либо продолжите с -after-id=%d): %w",
		res.LastID, res.LastID, err)
}

func (r *ImportResult) verbs() (written string) {
	if r.DryRun {
		return "было бы записано"
	}
	return "записано"
}

func (r *ImportResult) logProgress(logf Logf, elapsed time.Duration) {
	left := max(0, r.Total-r.Read)
	percent := 100.0
	if r.Total > 0 {
		percent = float64(r.Read) * 100 / float64(r.Total)
	}
	speed := float64(r.Read) / max(elapsed.Seconds(), 0.001)
	eta := "—"
	if speed > 0 && left > 0 {
		eta = (time.Duration(float64(left)/speed) * time.Second).Round(time.Second).String()
	}
	logf("ход: прочитано %d из %d (%.1f%%), %s %d, пропущено (уже есть) %d, конфликтов %d, осталось %d, скорость %.0f/с, ещё примерно %s",
		r.Read, r.Total, percent, r.verbs(), r.Inserted, r.Existing, r.Conflicts, left, speed, eta)
}

func (r *ImportResult) logSummary(logf Logf) {
	logf("ИТОГ: прочитано %d из %d, %s %d, пропущено (уже есть) %d, конфликтов %d, осталось 0; заняло %s",
		r.Read, r.Total, r.verbs(), r.Inserted, r.Existing, r.Conflicts, r.Elapsed.Round(time.Millisecond))
	if r.Conflicts > 0 {
		keys := make([]string, len(r.ConflictKeys))
		for i, k := range r.ConflictKeys {
			keys[i] = k.String()
		}
		logf("ВНИМАНИЕ: %d ссылок не перенесены — их код и домен уже заняты ссылкой с другим длинным адресом (дубль в самом источнике либо код успели занять в приёмнике). Первые: %s",
			r.Conflicts, strings.Join(keys, ", "))
	}
	logf("перенос шёл по ссылкам с id источника от %d до %d; что появится в Shlink позже, заберёт докачка (-new)", r.FromID+1, r.MaxID)
}

// findLastImported ищет самую новую ссылку источника, которая уже есть в приёмнике: идёт по
// источнику от новых ссылок к старым, пока не встретит перенесённую. Возвращает её id (0 — нет).
func findLastImported(ctx context.Context, src *source, dst *dest, maxID int64, batchSize int) (int64, error) {
	n, err := dst.count(ctx)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, nil
	}

	before := maxID + 1
	for before > 1 {
		if err = ctx.Err(); err != nil {
			return 0, err
		}
		keys, err := src.keysBefore(ctx, before, batchSize)
		if err != nil {
			return 0, err
		}
		if len(keys) == 0 {
			break
		}

		lookupKeys := make([]Key, len(keys))
		for i, k := range keys {
			lookupKeys[i] = k.Key
		}
		found, err := dst.lookup(ctx, lookupKeys)
		if err != nil {
			return 0, err
		}
		for _, k := range keys { // от новых к старым
			if _, ok := found[k.Key]; ok {
				return k.ID, nil
			}
		}
		before = keys[len(keys)-1].ID
	}
	return 0, nil
}

// PrintSurvey печатает обзор источника: что в базе Shlink есть и что из этого не переносится.
func PrintSurvey(ctx context.Context, srcDSN string, loc *time.Location, logf Logf) error {
	if loc == nil {
		return errors.New("не задан часовой пояс источника (в каком поясе работает Shlink)")
	}
	src, err := openSource(ctx, srcDSN, loc)
	if err != nil {
		return err
	}
	defer src.close(ctx)

	survey, err := src.survey(ctx)
	if err != nil {
		return safeError(err)
	}
	survey.log(logf)
	return nil
}
