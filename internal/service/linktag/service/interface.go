package service

import "context"

type linkServiceI interface {
	// TagAdd дописывает метки в справочник
	TagAdd(ctx context.Context, names []string) error
	// TagDropUnused убирает из справочника те из names, которых не осталось ни у одной ссылки
	TagDropUnused(ctx context.Context, names []string) (dropped []string, err error)
	// TagRecount сверяет справочник с таблицей ссылок, читая её целиком; done=false — пересчёт
	// уже идёт в другой копии сервиса
	TagRecount(ctx context.Context) (added, dropped []string, done bool, err error)
}
