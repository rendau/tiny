package link

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rendau/tiny/internal/domain/link/model"
	"github.com/rendau/tiny/internal/errs"
)

// Операции публичного сервера (совместимого с Shlink). Вошедший человек здесь не нужен: переход
// открыт всем, а /rest/* закрыт ключом API — его проверяет сам сервер.

// Redirect — адрес, на который ведёт короткая ссылка. host — заголовок Host запроса, requestQuery —
// строка параметров запроса короткой ссылки. Ссылки нет или она не действует — errs.ObjectNotFound.
func (u *Usecase) Redirect(ctx context.Context, host, shortCode, requestQuery string) (string, error) {
	link, found, err := u.svc.FindForRedirect(ctx, shortCode, u.requestDomain(host))
	if err != nil {
		return "", fmt.Errorf("svc.FindForRedirect: %w", err)
	}
	if !found || !isActive(link, time.Now()) {
		return "", errs.ObjectNotFound
	}

	return buildRedirectUrl(link.LongUrl, requestQuery, link.ForwardQuery), nil
}

// PublicCreate — POST /rest/v3/short-urls
func (u *Usecase) PublicCreate(ctx context.Context, req *model.CreateReq) (*model.Main, error) {
	return u.create(ctx, req)
}

// PublicGet — GET /rest/v3/short-urls/{код}. domain — параметр запроса: не задан или основной —
// ссылка основного домена, иначе ссылка ровно этого домена (без запасного поиска, как у Shlink).
// Срок действия не проверяется.
func (u *Usecase) PublicGet(ctx context.Context, shortCode string, domain *string) (*model.Main, error) {
	linkDomain := ""
	if domain != nil {
		linkDomain = u.requestDomain(*domain)
	}

	result, found, err := u.svc.GetByCode(ctx, shortCode, linkDomain, false)
	if err != nil {
		return nil, fmt.Errorf("svc.GetByCode: %w", err)
	}
	if !found {
		return nil, CodeError{
			ErrFull:   errs.ErrFull{Err: errs.ObjectNotFound, Desc: "short url not found"},
			ShortCode: shortCode,
			Domain:    linkDomain,
		}
	}

	return u.withShortUrl(result), nil
}

// Health — сервис может отвечать: база доступна
func (u *Usecase) Health(ctx context.Context) error {
	if err := u.svc.Ping(ctx); err != nil {
		return fmt.Errorf("svc.Ping: %w", err)
	}
	return nil
}

// requestDomain — домен запроса в виде хранения: основной домен — пустая строка. Порт не
// отбрасывается: Shlink сравнивает адрес целиком.
func (u *Usecase) requestDomain(host string) string {
	domain := strings.ToLower(strings.TrimSpace(host))
	if domain == u.cfg.DefaultDomain {
		return ""
	}
	return domain
}
