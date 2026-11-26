package loginlimit

// LoginLimiter — защита от перебора паролей: счёт неудачных входов по ключам (логин, адрес) за
// окно. Место под попытку занимается до проверки пароля и возвращается, если вход удался.
type LoginLimiter interface {
	Reserve(limits map[string]int) (release func(), ok bool)
}
