//go:build integration

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/tiny/internal/domain/link/linktest"
	"github.com/rendau/tiny/internal/domain/link/model"
	domainLinkRepoDb "github.com/rendau/tiny/internal/domain/link/repo/db"
	domainLinkService "github.com/rendau/tiny/internal/domain/link/service"
)

// На живой базе: о метках только сообщают, а в списке меток они появляются и пропадают сами
func TestKeeperOnRealBase(t *testing.T) {
	ctx := context.Background()
	link := domainLinkService.New(domainLinkRepoDb.New(linktest.PgPool(t)))

	svc := New(link)
	start(t, svc)

	tagsBecome := func(want ...string) {
		t.Helper()
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			tags, err := link.TagList(ctx)
			assert.NoError(c, err)
			assert.ElementsMatch(c, want, tags)
		}, 3*time.Second, 10*time.Millisecond)
	}

	id, err := link.Create(ctx, &model.Edit{LongUrl: new("https://example.com/a"), Tags: new([]string{"promo", "sms"})}, 5)
	require.NoError(t, err)
	svc.Changed([]string{"promo", "sms"}, nil)
	tagsBecome("promo", "sms")

	// метку сменили: новая появилась, прежней ни у кого не осталось — ушла
	require.NoError(t, link.Update(ctx, id, &model.Edit{Tags: new([]string{"news", "sms"})}))
	svc.Changed([]string{"news"}, []string{"promo"})
	tagsBecome("news", "sms")

	// сообщили «пропала», а метка у ссылки есть — остаётся
	svc.Changed(nil, []string{"sms"})
	svc.Changed([]string{"x"}, nil) // идёт следом: когда появится, предыдущее уже обработано
	tagsBecome("news", "sms", "x")

	// ссылку удалили
	require.NoError(t, link.Delete(ctx, id))
	svc.Changed(nil, []string{"news", "sms"})
	tagsBecome("x")
}
