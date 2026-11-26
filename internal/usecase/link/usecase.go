package link

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/rendau/tiny/internal/domain/link/model"
	"github.com/rendau/tiny/internal/errs"
	"github.com/rendau/tiny/internal/util"
)

// Config — настройки, от которых зависят ссылки (имена и смысл — как у Shlink)
type Config struct {
	// DefaultDomain — основной домен: с ним собирается короткий адрес ссылок без своего домена
	DefaultDomain string
	// HttpsEnabled — схема короткого адреса: https или http
	HttpsEnabled bool
	// DefaultCodeLength — длина генерируемого кода, если в запросе не указана
	DefaultCodeLength int
}

type Usecase struct {
	svc        ServiceI
	sessionSvc SessionServiceI
	tagKeeper  TagKeeperI
	cfg        Config
}

func New(svc ServiceI, sessionSvc SessionServiceI, tagKeeper TagKeeperI, cfg Config) *Usecase {
	cfg.DefaultDomain = strings.ToLower(strings.TrimSpace(cfg.DefaultDomain))
	if cfg.DefaultCodeLength < MinShortCodeLength {
		cfg.DefaultCodeLength = MinShortCodeLength
	}

	return &Usecase{svc: svc, sessionSvc: sessionSvc, tagKeeper: tagKeeper, cfg: cfg}
}

// Операции админки — только вошедшему человеку

func (u *Usecase) List(ctx context.Context, pars *model.ListReq) ([]*model.Main, int64, error) {
	if err := u.requireAuthorized(ctx); err != nil {
		return nil, 0, err
	}
	if err := util.RequirePageSize(pars.ListParams, 0); err != nil {
		return nil, 0, err
	}

	if pars.Domain != nil {
		// основной домен хранится пустой строкой; неверное имя — заведомо ничего не найдётся
		domain, ok := u.normalizeDomain(*pars.Domain)
		if !ok && strings.TrimSpace(*pars.Domain) != "" {
			return []*model.Main{}, 0, nil
		}
		pars.Domain = &domain
	}

	items, tCount, err := u.svc.List(ctx, pars)
	if err != nil {
		return nil, 0, fmt.Errorf("svc.List: %w", err)
	}

	return lo.Map(items, func(item *model.Main, _ int) *model.Main { return u.withShortUrl(item) }), tCount, nil
}

func (u *Usecase) Get(ctx context.Context, id int64) (*model.Main, error) {
	if err := u.requireAuthorized(ctx); err != nil {
		return nil, err
	}

	result, _, err := u.svc.Get(ctx, id, true)
	if err != nil {
		return nil, fmt.Errorf("svc.Get: %w", err)
	}

	return u.withShortUrl(result), nil
}

func (u *Usecase) Create(ctx context.Context, req *model.CreateReq) (*model.Main, error) {
	if err := u.requireAuthorized(ctx); err != nil {
		return nil, err
	}

	return u.create(ctx, req)
}

// Update — полная замена изменяемых полей; код и домен ссылки не меняются
func (u *Usecase) Update(ctx context.Context, id int64, obj *model.Edit) error {
	if err := u.requireAuthorized(ctx); err != nil {
		return err
	}
	if id == 0 {
		return errs.IdRequired
	}
	if err := u.validateEdit(obj); err != nil {
		return err
	}

	// прежние метки — только чтобы потом сообщить справочнику, какие пропали
	var oldTags []string
	if obj.Tags != nil {
		oldTags = u.tagsOf(ctx, id)
	}

	if err := u.svc.Update(ctx, id, obj); err != nil {
		return fmt.Errorf("svc.Update: %w", err)
	}

	if obj.Tags != nil {
		removed, added := lo.Difference(oldTags, *obj.Tags)
		u.tagKeeper.Changed(added, removed)
	}
	return nil
}

func (u *Usecase) Delete(ctx context.Context, id int64) error {
	if err := u.requireAuthorized(ctx); err != nil {
		return err
	}
	if id == 0 {
		return errs.IdRequired
	}

	// метки ссылки — только чтобы потом сообщить справочнику, что они могли пропасть
	oldTags := u.tagsOf(ctx, id)

	if err := u.svc.Delete(ctx, id); err != nil {
		return fmt.Errorf("svc.Delete: %w", err)
	}

	u.tagKeeper.Changed(nil, oldTags)
	return nil
}

// tagsOf — метки ссылки сейчас. Нужны только справочнику меток, поэтому сбой чтения ничего не
// отменяет: меток «нет», справочник поправит пересчёт.
func (u *Usecase) tagsOf(ctx context.Context, id int64) []string {
	link, found, err := u.svc.Get(ctx, id, false)
	if err != nil || !found {
		return nil
	}
	return link.Tags
}

// TagList — метки, которые встречаются у ссылок; search — подстрока в названии метки
func (u *Usecase) TagList(ctx context.Context, search *string) ([]string, error) {
	if err := u.requireAuthorized(ctx); err != nil {
		return nil, err
	}

	tags, err := u.svc.TagList(ctx)
	if err != nil {
		return nil, fmt.Errorf("svc.TagList: %w", err)
	}

	if search == nil || strings.TrimSpace(*search) == "" {
		return tags, nil
	}

	needle := strings.ToLower(strings.TrimSpace(*search))

	return lo.Filter(tags, func(tag string, _ int) bool {
		return strings.Contains(strings.ToLower(tag), needle)
	}), nil
}

func (u *Usecase) requireAuthorized(ctx context.Context) error {
	if !u.sessionSvc.FromContext(ctx).IsAuthorized() {
		return errs.NotAuthorized
	}
	return nil
}

// validateEdit приводит правку из админки к виду хранения и проверяет переданные поля
func (u *Usecase) validateEdit(obj *model.Edit) error {
	// код и домен — адрес ссылки: после создания не меняются
	obj.ShortCode, obj.Domain, obj.CreatedAt = nil, nil, nil

	if obj.LongUrl != nil {
		if msg := validateLongUrl(*obj.LongUrl); msg != "" {
			return errs.ErrFull{Err: errs.InvalidRequest, Desc: "invalid link data", Fields: map[string]string{FieldLongUrl: msg}}
		}
	}
	if obj.Title != nil {
		obj.Title = new(normalizeTitle(*obj.Title))
	}
	if obj.Tags != nil {
		obj.Tags = new(storedTags(normalizeTags(*obj.Tags)))
	}
	if obj.ValidSince != nil {
		obj.ValidSince = new(truncateToSecond(*obj.ValidSince))
	}
	if obj.ValidUntil != nil {
		obj.ValidUntil = new(truncateToSecond(*obj.ValidUntil))
	}

	return nil
}

// ValidateCreate проверяет запрос на создание, ничего не создавая: nil или errs.ErrFull
// (errs.InvalidRequest) со всеми неверными полями
func (u *Usecase) ValidateCreate(req *model.CreateReq) error {
	_, err := u.normalizeCreate(req)
	return err
}

// CodeError — отказ про конкретную ссылку: код занят (errs.ShortCodeExists) или ссылки с таким
// кодом нет (errs.ObjectNotFound). Несёт код и домен в виде хранения (основной домен — пусто):
// публичному серверу они нужны для ответа в виде Shlink. Для остальных это обычный errs.ErrFull.
type CodeError struct {
	ErrFull   errs.ErrFull
	ShortCode string
	Domain    string
}

func (e CodeError) Error() string {
	return e.ErrFull.Error()
}

func (e CodeError) Unwrap() error {
	return e.ErrFull
}

// create — общее создание для админки и публичного сервера, правила — как у Shlink
func (u *Usecase) create(ctx context.Context, req *model.CreateReq) (*model.Main, error) {
	n, err := u.normalizeCreate(req)
	if err != nil {
		return nil, err
	}

	if req.FindIfExists {
		existing, found, err := u.svc.FindExisting(ctx, n.findExistingReq())
		if err != nil {
			return nil, fmt.Errorf("svc.FindExisting: %w", err)
		}
		if found {
			return u.withShortUrl(existing), nil
		}
	}

	newId, err := u.svc.Create(ctx, n.edit(), n.codeLength)
	if err != nil {
		if errors.Is(err, errs.ShortCodeExists) {
			return nil, CodeError{
				ErrFull:   errs.ErrFull{Err: errs.ShortCodeExists, Desc: "short code is already in use", Fields: map[string]string{FieldShortCode: "exists"}},
				ShortCode: n.shortCode,
				Domain:    n.domain,
			}
		}
		return nil, fmt.Errorf("svc.Create: %w", err)
	}

	// ссылка записана — её метки дописываются в справочник меток. Отдельно и в фоне: ответ этого
	// не ждёт, сбой справочника на создание не влияет
	u.tagKeeper.Changed(n.tags, nil)

	// ссылка уже записана: читаем её для ответа, даже если клиент перестал ждать, — иначе
	// состоявшееся создание выглядело бы сбоем
	result, _, err := u.svc.Get(context.WithoutCancel(ctx), newId, true)
	if err != nil {
		return nil, fmt.Errorf("svc.Get: %w", err)
	}

	// в ответе на создание Shlink отдаёт метки в порядке запроса (при чтении — по алфавиту)
	result.Tags = n.tags

	return u.withShortUrl(result), nil
}

// findExistingReq — что сравнивать при findIfExists: длинный адрес и только названные в запросе
// параметры (как ShortUrlRepository::findOneMatching у Shlink)
func (n *normalizedCreate) findExistingReq() *model.FindExistingReq {
	result := &model.FindExistingReq{
		LongUrl:    n.longUrl,
		ValidSince: n.validSince,
		ValidUntil: n.validUntil,
		MaxVisits:  n.maxVisits,
		Tags:       n.tags,
	}
	if n.shortCode != "" {
		result.ShortCode = &n.shortCode
	}
	if n.domainGiven {
		result.Domain = &n.domain
	}
	return result
}

func (n *normalizedCreate) edit() *model.Edit {
	result := &model.Edit{
		Domain:       &n.domain,
		LongUrl:      &n.longUrl,
		Title:        &n.title,
		Tags:         new(storedTags(n.tags)),
		ValidSince:   &n.validSince,
		ValidUntil:   &n.validUntil,
		MaxVisits:    &n.maxVisits,
		ForwardQuery: &n.forwardQuery,
	}
	if n.shortCode != "" {
		result.ShortCode = &n.shortCode
	}
	return result
}

// withShortUrl дописывает полный короткий адрес
func (u *Usecase) withShortUrl(link *model.Main) *model.Main {
	link.ShortUrl = u.shortUrl(link.Domain, link.ShortCode)
	return link
}

func (u *Usecase) shortUrl(domain, shortCode string) string {
	scheme := "http"
	if u.cfg.HttpsEnabled {
		scheme = "https"
	}
	if domain == "" {
		domain = u.cfg.DefaultDomain
	}
	return scheme + "://" + domain + "/" + shortCode
}

// isActive — ссылка действует: началась, не истекла и не исчерпала число переходов. Посещения
// не считаются (как при DISABLE_TRACKING у Shlink), поэтому предел переходов срабатывает только
// когда он не больше нуля — ровно как у Shlink с выключенным учётом.
func isActive(link *model.Main, now time.Time) bool {
	if link.MaxVisits != nil && *link.MaxVisits <= 0 {
		return false
	}
	if link.ValidSince != nil && link.ValidSince.After(now) {
		return false
	}
	if link.ValidUntil != nil && link.ValidUntil.Before(now) {
		return false
	}
	return true
}
