package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/rendau/tiny/internal/domain/link/model"
	"github.com/rendau/tiny/internal/errs"
)

const (
	// codeAlphabet — символы генерируемого кода, как у Shlink
	codeAlphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

	// generateAttempts — сколько раз подбирать свободный код. При длине 4 (14,7 млн кодов) и
	// миллионах ссылок совпадения часты, но 20 неудач подряд практически невозможны.
	generateAttempts = 20

	// listCacheTTL — сколько помнить списки, ради которых читается вся таблица (метки, robots.txt)
	listCacheTTL = time.Minute
)

type Service struct {
	repoDb RepoDbI

	tags      *cachedList
	crawlable *cachedList
}

func New(repoDb RepoDbI) *Service {
	return &Service{
		repoDb:    repoDb,
		tags:      newCachedList(listCacheTTL),
		crawlable: newCachedList(listCacheTTL),
	}
}

func (s *Service) List(ctx context.Context, pars *model.ListReq) ([]*model.Main, int64, error) {
	items, tCount, err := s.repoDb.List(ctx, pars)
	if err != nil {
		return nil, 0, fmt.Errorf("repoDb.List: %w", err)
	}
	return items, tCount, nil
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
		s.invalidateListsOnCreate(obj)
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
		s.invalidateListsOnCreate(obj)
		return newId, nil
	}

	return 0, fmt.Errorf("no free short code of length %d after %d attempts", codeLength, generateAttempts)
}

func (s *Service) Update(ctx context.Context, id int64, obj *model.Edit) error {
	obj.ModifiedAt = new(time.Now())

	defer s.invalidateLists()

	if err := s.repoDb.Update(ctx, id, obj); err != nil {
		return fmt.Errorf("repoDb.Update: %w", err)
	}
	return nil
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	defer s.invalidateLists()

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

// TagList — все метки ссылок по алфавиту (до listCacheTTL из памяти)
func (s *Service) TagList(ctx context.Context) ([]string, error) {
	result, err := s.tags.get(ctx, s.repoDb.TagList)
	if err != nil {
		return nil, fmt.Errorf("repoDb.TagList: %w", err)
	}
	return result, nil
}

// CrawlableCodes — коды ссылок, открытых поисковикам (до listCacheTTL из памяти)
func (s *Service) CrawlableCodes(ctx context.Context) ([]string, error) {
	result, err := s.crawlable.get(ctx, s.repoDb.CrawlableCodes)
	if err != nil {
		return nil, fmt.Errorf("repoDb.CrawlableCodes: %w", err)
	}
	return result, nil
}

func (s *Service) Ping(ctx context.Context) error {
	if err := s.repoDb.Ping(ctx); err != nil {
		return fmt.Errorf("repoDb.Ping: %w", err)
	}
	return nil
}

// invalidateLists — ссылку изменили или удалили: оба списка могли измениться
func (s *Service) invalidateLists() {
	s.tags.invalidate()
	s.crawlable.invalidate()
}

// invalidateListsOnCreate — ссылка добавлена. Списки сбрасываются, только если она их меняет:
// принесла метку, которой ещё не было, или открыта поисковикам. Ссылки создаются постоянно, и
// сброс на каждую заставлял бы читать всю таблицу при каждом запросе списка.
func (s *Service) invalidateListsOnCreate(obj *model.Edit) {
	if obj.Tags != nil {
		s.tags.invalidateIfMissing(*obj.Tags)
	}
	if obj.Crawlable != nil && *obj.Crawlable {
		s.crawlable.invalidate()
	}
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
