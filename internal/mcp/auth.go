package mcp

import (
	"context"
	"net/http"
	"strings"
)

// Authorizer gives the requests to a server reached at a URL the token of
// the person's sign-in to it (LoginStore), and renews it when the server
// refuses it: with the refresh token, when there is one. A server reached
// over plain http elsewhere than this machine is never given one, nor is
// a server whose Authorization header is configured: the configured
// credential is the one in force (StartHTTP).
type Authorizer struct {
	// Logins is where the sign-ins are kept, and Server the server's URL,
	// the sign-in's key.
	Logins *LoginStore
	Server string
	// HTTP makes the refresh's request; nil is Go's default client.
	HTTP *http.Client
}

// token is the access token to send, or "" when there is no sign-in.
func (a *Authorizer) token() string {
	if !Secure(a.Server) {
		return ""
	}
	l, ok := a.Logins.Get(a.Server)
	if !ok {
		return ""
	}
	return l.AccessToken
}

// renew is told that the server refused (401) the token refused, and
// reports whether there is another to try. Under the store's lock, what
// is kept is read again: a token another session got meanwhile is tried;
// otherwise the refresh token is used. Only an authorization server's
// invalid_grant (the refresh token is no good) forgets the tokens, and a
// refused token with no refresh token; a refresh that failed on the way
// (the network, a timeout, the server's own error) keeps them for the
// next try.
func (a *Authorizer) renew(ctx context.Context, refused string) bool {
	try := false
	_ = a.Logins.Update(a.Server, func(l Login, ok bool) (Login, bool) {
		switch {
		case !ok || l.AccessToken == "":
			return l, ok
		case l.AccessToken != refused:
			try = true
			return l, true
		case l.RefreshToken == "":
			l.AccessToken = ""
			return l, true
		}
		t, err := Refresh(ctx, a.HTTP, l)
		switch {
		case err == nil:
			l.Token, try = t, true
		case IsInvalidGrant(err):
			l.AccessToken, l.RefreshToken = "", ""
		}
		return l, true
	})
	return try
}

// hasHeader reports whether headers name the header key, in any case.
func hasHeader(headers map[string]string, key string) bool {
	for k := range headers {
		if strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}
