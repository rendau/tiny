package shlinkimport

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
)

const DefaultVerifySample = 1000

// VerifyOptions — настройки проверки после переноса.
type VerifyOptions struct {
	SrcDSN string // база Shlink; только читается
	DstDSN string // база tiny; только читается

	SrcLocation *time.Location // пояс, в котором Shlink хранит время. Обязателен.

	Sample int  // сколько случайных ссылок сверить по всем полям; 0 — DefaultVerifySample
	All    bool // сверить все ссылки, а не выборку

	BatchSize     int
	ProgressEvery time.Duration
	Logf          Logf
}

// VerifyProblem — расхождение по одной ссылке.
type VerifyProblem struct {
	Key     Key
	SrcID   int64
	Missing bool     // в приёмнике такой ссылки нет
	Fields  []string // поля, которые отличаются
}

// VerifyResult — итог проверки.
type VerifyResult struct {
	SrcLinks        int64 // ссылок в источнике
	SrcDistinctKeys int64 // разных пар «код + домен» в источнике
	DstLinks        int64 // ссылок в приёмнике

	Checked       int64 // сверено ссылок
	Matched       int64 // совпали поле в поле
	Missing       int64 // нет в приёмнике
	Mismatched    int64 // есть, но отличаются
	SrcDuplicates int64 // дубли в самом источнике (не переносятся, не сверяются)

	Problems []VerifyProblem // первые расхождения (не больше maxReportedKeys)

	OK      bool
	Elapsed time.Duration
}

// Verify сверяет приёмник с источником: число ссылок и поля ссылок — случайной выборки или всех.
// Ни в одну из баз не пишет.
func Verify(ctx context.Context, opts VerifyOptions) (*VerifyResult, error) {
	if opts.SrcLocation == nil {
		return nil, errors.New("не задан часовой пояс источника (в каком поясе работает Shlink)")
	}
	if opts.Sample < 0 || opts.BatchSize < 0 {
		return nil, errors.New("размер выборки и пачки не могут быть отрицательными")
	}
	if opts.Sample == 0 {
		opts.Sample = DefaultVerifySample
	}
	if opts.BatchSize == 0 {
		opts.BatchSize = DefaultBatchSize
	}
	if opts.ProgressEvery == 0 {
		opts.ProgressEvery = DefaultProgressEvery
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	res, err := doVerify(ctx, opts)
	return res, safeError(err)
}

func doVerify(ctx context.Context, opts VerifyOptions) (*VerifyResult, error) {
	logf := opts.Logf
	started := time.Now()

	src, err := openSource(ctx, opts.SrcDSN, opts.SrcLocation)
	if err != nil {
		return nil, err
	}
	defer src.close(ctx)

	dst, err := openDest(ctx, opts.DstDSN, true)
	if err != nil {
		return nil, err
	}
	defer dst.close(ctx)

	logf("ПРОВЕРКА: ни в одну из баз ничего не пишется; время в источнике читается в поясе %s", opts.SrcLocation)

	survey, err := src.survey(ctx)
	if err != nil {
		return nil, err
	}
	survey.log(logf)

	res := &VerifyResult{SrcLinks: survey.Links, SrcDistinctKeys: survey.DistinctKeys}
	res.DstLinks, err = dst.count(ctx)
	if err != nil {
		return nil, err
	}
	logf("число ссылок: в источнике %d (разных пар «код + домен» %d), в приёмнике %d",
		res.SrcLinks, res.SrcDistinctKeys, res.DstLinks)

	check := func(rows []srcRow) error {
		keys := make([]Key, len(rows))
		for i := range rows {
			keys[i] = rows[i].Key()
		}
		found, err := dst.lookup(ctx, keys)
		if err != nil {
			return err
		}
		for i := range rows {
			res.compare(&rows[i], found[rows[i].Key()])
		}
		return nil
	}

	buf := make([]srcRow, 0, opts.BatchSize)
	if opts.All {
		maxID, err := src.maxID(ctx)
		if err != nil {
			return nil, err
		}
		sc := scope{maxID: maxID}
		logf("сверяются все ссылки источника по всем полям")

		var (
			lastID       int64
			lastProgress = time.Now()
		)
		for {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			buf, err = src.readBatch(ctx, sc, lastID, opts.BatchSize, true, buf)
			if err != nil {
				return nil, err
			}
			if len(buf) == 0 {
				break
			}
			if err = check(buf); err != nil {
				return nil, err
			}
			lastID = buf[len(buf)-1].SrcID

			if time.Since(lastProgress) >= opts.ProgressEvery {
				lastProgress = time.Now()
				logf("ход проверки: сверено %d из %d, совпало %d, нет в приёмнике %d, отличаются %d, осталось %d",
					res.Checked+res.SrcDuplicates, res.SrcLinks, res.Matched, res.Missing, res.Mismatched,
					max(0, res.SrcLinks-res.Checked-res.SrcDuplicates))
			}
			if len(buf) < opts.BatchSize {
				break
			}
		}
	} else {
		ids, err := src.sampleIDs(ctx, opts.Sample)
		if err != nil {
			return nil, err
		}
		logf("сверяется случайная выборка: %d ссылок по всем полям", len(ids))

		for chunk := range slices.Chunk(ids, opts.BatchSize) {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			buf, err = src.readByIDs(ctx, chunk, buf)
			if err != nil {
				return nil, err
			}
			if err = check(buf); err != nil {
				return nil, err
			}
		}
	}

	res.OK = res.Missing == 0 && res.Mismatched == 0 && res.DstLinks >= res.SrcDistinctKeys
	res.Elapsed = time.Since(started)
	res.logSummary(logf)
	return res, nil
}

func (r *VerifyResult) compare(src *srcRow, dst *Link) {
	if src.IsDup {
		// более поздняя строка с уже занятым кодом: в приёмнике лежит самая ранняя
		r.SrcDuplicates++
		return
	}
	r.Checked++

	problem := VerifyProblem{Key: src.Key(), SrcID: src.SrcID}
	switch {
	case dst == nil:
		r.Missing++
		problem.Missing = true
	default:
		problem.Fields = src.diff(dst)
		if len(problem.Fields) == 0 {
			r.Matched++
			return
		}
		r.Mismatched++
	}
	if len(r.Problems) < maxReportedKeys {
		r.Problems = append(r.Problems, problem)
	}
}

func (r *VerifyResult) logSummary(logf Logf) {
	logf("ИТОГ ПРОВЕРКИ: сверено %d, совпало поле в поле %d, нет в приёмнике %d, отличаются %d, дублей в источнике пропущено %d; заняло %s",
		r.Checked, r.Matched, r.Missing, r.Mismatched, r.SrcDuplicates, r.Elapsed.Round(time.Millisecond))

	for _, p := range r.Problems {
		if p.Missing {
			logf("  нет в приёмнике: %s (id источника %d)", p.Key, p.SrcID)
		} else {
			logf("  отличается: %s (id источника %d) — поля: %s", p.Key, p.SrcID, strings.Join(p.Fields, ", "))
		}
	}
	if r.DstLinks < r.SrcDistinctKeys {
		logf("в приёмнике ссылок меньше, чем в источнике: не хватает как минимум %d", r.SrcDistinctKeys-r.DstLinks)
	}

	if r.OK {
		logf("ПРОВЕРКА ПРОЙДЕНА")
		return
	}
	logf("ПРОВЕРКА НЕ ПРОЙДЕНА. Если Shlink ещё принимает новые ссылки — сначала докачайте их (-new) и повторите проверку")
}
