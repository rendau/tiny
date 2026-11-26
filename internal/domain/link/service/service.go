package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/rendau/tiny/internal/domain/link/model"
	"github.com/rendau/tiny/internal/errs"
)

const (
	// codeAlphabet — символы генерируемого кода, как у Shlink
	codeAlphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

	// generateAttempts — сколько раз подбирать свободный код. При длине 4 (14,7 млн кодов) и
	// миллионах ссылок совпадения часты, но 20 неудач подряд практически невозможны.
	generateAttempts = 20

	// totalCountCap — дальше этого числа найденные по фильтру ссылки не считаются
	totalCountCap = 10_000
)

type Service struct {
	repoDb RepoDbI
}

func New(repoDb RepoDbI) *Service {
	return &Service{repoDb: repoDb}
}

// List — ссылки и их общее число. capped — подсчёт остановлен на потолке totalCountCap:
// ссылок не меньше, чем возвращённое число (в админке — «больше 10 000»).
func (s *Service) List(ctx context.Context, pars *model.ListReq) (items []*model.Main, tCount int64, capped bool, err error) {
	if pars.WithTotalCount || pars.OnlyCount {
		switch {
		case isActiveOnly(pars):
			items, tCount, err = s.listActive(ctx, pars)
			return items, tCount, false, err
		case !isExactCountCheap(pars):
			return s.listCapped(ctx, pars)
		}
	}

	items, tCount, err = s.repoDb.List(ctx, pars)
	if err != nil {
		return nil, 0, false, fmt.Errorf("repoDb.List: %w", err)
	}
	return items, tCount, false, nil
}

// isActiveOnly — фильтр «только действующие» и ничего больше
func isActiveOnly(pars *model.ListReq) bool {
	return pars.Expired != nil && !*pars.Expired && noRowFilters(pars)
}

// isExactCountCheap — точное число считается по одному индексу, таблица не читается:
// фильтра нет вовсе (первичный ключ) или он «только истёкшие» (link_valid_until_idx)
func isExactCountCheap(pars *model.ListReq) bool {
	return (pars.Expired == nil || *pars.Expired) && noRowFilters(pars)
}

// noRowFilters — нет фильтров, кроме срока действия
func noRowFilters(pars *model.ListReq) bool {
	return pars.Tag == nil && pars.ShortCode == nil && pars.Domain == nil &&
		(pars.Search == nil || *pars.Search == "")
}

// listCapped — список с фильтром и число найденного, но не дальше totalCountCap. Точное число
// стоит слишком дорого: чтобы посчитать ссылки с меткой, база читает с диска каждую страницу
// таблицы, где такая ссылка есть, а у массовой метки это вся таблица (индекс даёт только адреса
// строк). С потолком чтение обрывается, как только набралось totalCountCap+1 строк.
func (s *Service) listCapped(ctx context.Context, pars *model.ListReq) ([]*model.Main, int64, bool, error) {
	var (
		items = []*model.Main{}
		count int64
	)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		if count, err = s.repoDb.CountUpTo(gctx, pars, totalCountCap+1); err != nil {
			return fmt.Errorf("repoDb.CountUpTo: %w", err)
		}
		return nil
	})
	if !pars.OnlyCount {
		g.Go(func() error {
			listPars := *pars
			listPars.WithTotalCount = false

			var err error
			if items, _, err = s.repoDb.List(gctx, &listPars); err != nil {
				return fmt.Errorf("repoDb.List: %w", err)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, 0, false, err
	}

	if count > totalCountCap {
		return items, totalCountCap, true, nil
	}
	return items, count, false, nil
}

// listActive — список действующих ссылок с их числом. Число считается как «все минус истёкшие»:
// у действующей ссылки срок либо не задан, либо ещё не вышел, и на такое условие («или») индекса
// нет — подсчёт в лоб читает всю таблицу (секунды на миллионах ссылок). А «все» и «истёкшие»
// считаются каждое по своему индексу, одновременно.
func (s *Service) listActive(ctx context.Context, pars *model.ListReq) ([]*model.Main, int64, error) {
	var (
		items        = []*model.Main{}
		all, expired int64
	)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		if all, err = s.repoDb.CountAll(gctx); err != nil {
			return fmt.Errorf("repoDb.CountAll: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		var err error
		if expired, err = s.repoDb.CountExpired(gctx); err != nil {
			return fmt.Errorf("repoDb.CountExpired: %w", err)
		}
		return nil
	})
	if !pars.OnlyCount {
		g.Go(func() error {
			listPars := *pars
			listPars.WithTotalCount = false

			var err error
			if items, _, err = s.repoDb.List(gctx, &listPars); err != nil {
				return fmt.Errorf("repoDb.List: %w", err)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, 0, err
	}

	// два подсчёта сделаны не в один миг: между ними ссылка могла истечь
	return items, max(all-expired, 0), nil
}

func (s *Service) Get(ctx context.Context, id int64, errNE bool) (*model.Main, bool, error) {
	result, found, err := s.repoDb.Get(ctx, id)
	if err != nil {
		return nil, false, fmt.Errorf("repoDb.Get: %w", err)
	}
	if !found {
		if errNE {
			return nil, false, errs.ObjectNotFound
		}
		return nil, false, nil
	}
	return result, true, nil
}

// Create создаёт ссылку. Код задан в obj.ShortCode — берётся он (занят — errs.ShortCodeExists);
// не задан — подбирается свободный случайный длиной codeLength.
func (s *Service) Create(ctx context.Context, obj *model.Edit, codeLength int) (int64, error) {
	obj.ModifiedAt = nil // DB-default устанавливает значение

	if obj.ShortCode != nil && *obj.ShortCode != "" {
		newId, err := s.repoDb.Create(ctx, obj)
		if err != nil {
			return 0, fmt.Errorf("repoDb.Create: %w", err)
		}
		return newId, nil
	}

	for range generateAttempts {
		code, err := GenerateCode(codeLength)
		if err != nil {
			return 0, fmt.Errorf("GenerateCode: %w", err)
		}
		obj.ShortCode = &code

		newId, err := s.repoDb.Create(ctx, obj)
		if err != nil {
			if errors.Is(err, errs.ShortCodeExists) {
				continue // код занят — пробуем другой
			}
			return 0, fmt.Errorf("repoDb.Create: %w", err)
		}
		return newId, nil
	}

	return 0, fmt.Errorf("no free short code of length %d after %d attempts", codeLength, generateAttempts)
}

func (s *Service) Update(ctx context.Context, id int64, obj *model.Edit) error {
	obj.ModifiedAt = new(time.Now())

	if err := s.repoDb.Update(ctx, id, obj); err != nil {
		return fmt.Errorf("repoDb.Update: %w", err)
	}
	return nil
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	if err := s.repoDb.Delete(ctx, id); err != nil {
		return fmt.Errorf("repoDb.Delete: %w", err)
	}
	return nil
}

// GetByCode — ссылка ровно этого домена (пустая строка — основной)
func (s *Service) GetByCode(ctx context.Context, shortCode, domain string, errNE bool) (*model.Main, bool, error) {
	result, found, err := s.repoDb.GetByCode(ctx, shortCode, domain)
	if err != nil {
		return nil, false, fmt.Errorf("repoDb.GetByCode: %w", err)
	}
	if !found {
		if errNE {
			return nil, false, errs.ObjectNotFound
		}
		return nil, false, nil
	}
	return result, true, nil
}

// FindForRedirect — ссылка для перехода: сначала ссылка домена запроса, иначе — основного домена
func (s *Service) FindForRedirect(ctx context.Context, shortCode, domain string) (*model.Main, bool, error) {
	result, found, err := s.repoDb.FindForRedirect(ctx, shortCode, domain)
	if err != nil {
		return nil, false, fmt.Errorf("repoDb.FindForRedirect: %w", err)
	}
	return result, found, nil
}

// FindExisting — уже существующая ссылка с такими параметрами (самая старая)
func (s *Service) FindExisting(ctx context.Context, pars *model.FindExistingReq) (*model.Main, bool, error) {
	result, found, err := s.repoDb.FindExisting(ctx, pars)
	if err != nil {
		return nil, false, fmt.Errorf("repoDb.FindExisting: %w", err)
	}
	return result, found, nil
}

// TagList — все метки ссылок по алфавиту (из справочника меток, таблица ссылок не читается)
func (s *Service) TagList(ctx context.Context) ([]string, error) {
	result, err := s.repoDb.TagList(ctx)
	if err != nil {
		return nil, fmt.Errorf("repoDb.TagList: %w", err)
	}
	return result, nil
}

// Справочник меток с записью ссылок не связан: Create, Update и Delete его не трогают.
// Дописывает и чистит его фоновый сервис (internal/service/linktag) методами ниже.

// TagAdd дописывает метки в справочник
func (s *Service) TagAdd(ctx context.Context, names []string) error {
	if err := s.repoDb.TagAdd(ctx, names); err != nil {
		return fmt.Errorf("repoDb.TagAdd: %w", err)
	}
	return nil
}

// TagDropUnused убирает из справочника те из names, которых не осталось ни у одной ссылки
func (s *Service) TagDropUnused(ctx context.Context, names []string) ([]string, error) {
	dropped, err := s.repoDb.TagDropUnused(ctx, names)
	if err != nil {
		return nil, fmt.Errorf("repoDb.TagDropUnused: %w", err)
	}
	return dropped, nil
}

// TagRecount сверяет справочник меток с таблицей ссылок (читает её целиком): что дописано, что
// убрано. done=false — пересчёт уже идёт в другой копии сервиса.
func (s *Service) TagRecount(ctx context.Context) (added, dropped []string, done bool, err error) {
	added, dropped, done, err = s.repoDb.TagRecount(ctx)
	if err != nil {
		return nil, nil, false, fmt.Errorf("repoDb.TagRecount: %w", err)
	}
	return added, dropped, done, nil
}

func (s *Service) Ping(ctx context.Context) error {
	if err := s.repoDb.Ping(ctx); err != nil {
		return fmt.Errorf("repoDb.Ping: %w", err)
	}
	return nil
}

// GenerateCode — случайный код из codeAlphabet. Байты берутся из crypto/rand; байт, который дал бы
// перекос (256 не делится на 62), отбрасывается — все символы равновероятны.
func GenerateCode(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("bad code length %d", length)
	}

	const maxUnbiased = 256 - 256%len(codeAlphabet) // 248

	result := make([]byte, 0, length)
	buf := make([]byte, length+length/2+4)

	for len(result) < length {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("rand.Read: %w", err)
		}
		for _, b := range buf {
			if int(b) >= maxUnbiased {
				continue
			}
			result = append(result, codeAlphabet[int(b)%len(codeAlphabet)])
			if len(result) == length {
				break
			}
		}
	}

	return string(result), nil
}
