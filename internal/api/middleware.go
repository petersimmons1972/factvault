package api

import (
	"context"
	"net/http"

	"github.com/petersimmons1972/factvault/internal/auth"
)

type claimsContextKey struct{}

func (s *Server) jwtMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := auth.BearerToken(r.Header.Get("Authorization"))
		if !ok {
			writeProblem(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		claims, err := s.Verifier.Verify(token)
		if err != nil {
			writeProblem(w, http.StatusUnauthorized, "unauthorized", err.Error())
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), claimsContextKey{}, claims)))
	})
}

// ClaimsFromContext extracts auth.Claims from request context.
func ClaimsFromContext(ctx context.Context) auth.Claims {
	claims, ok := ctx.Value(claimsContextKey{}).(auth.Claims)
	if !ok {
		return auth.Claims{}
	}
	return claims
}
