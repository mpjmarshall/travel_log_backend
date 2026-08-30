package admin

import "context"

type ctxKey int

const csrfKey ctxKey = iota

func withCSRF(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, csrfKey, token)
}

// csrfFrom answers the session's token, or empty when there is no session.
func csrfFrom(ctx context.Context) string {
	token, _ := ctx.Value(csrfKey).(string)
	return token
}
