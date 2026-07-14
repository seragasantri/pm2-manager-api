package httpx

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/tragasolusi/pm2-manager-api/internal/auth"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

// ctxKey is a private type to avoid context key collisions.
const claimsKey = "auth.claims"

// Authenticate verifies the bearer token and stashes claims in the context.
func Authenticate(svc *auth.Service) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			h := c.Request().Header.Get("Authorization")
			if h == "" {
				return response.Unauthorized(c, "token tidak ditemukan")
			}
			parts := strings.SplitN(h, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				return response.Unauthorized(c, "format token salah")
			}
			claims, err := svc.Verify(parts[1])
			if err != nil {
				if err == auth.ErrExpired {
					return response.Unauthorized(c, "sesi kadaluarsa")
				}
				return response.Unauthorized(c, "token tidak valid")
			}
			c.Set(claimsKey, claims)
			return next(c)
		}
	}
}

// RequireRole returns middleware that allows only the given role.
func RequireRole(role string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			claims := ClaimsFrom(c)
			if claims == nil || claims.Role != role {
				return response.Forbidden(c, "akses hanya untuk "+role)
			}
			return next(c)
		}
	}
}

// ClaimsFrom retrieves the auth claims set by Authenticate.
func ClaimsFrom(c echo.Context) *auth.Claims {
	v := c.Get(claimsKey)
	if v == nil {
		return nil
	}
	cl, _ := v.(*auth.Claims)
	return cl
}

// JWTFromQuery extracts a JWT from ?token= for WebSocket upgrade where headers can't be set easily.
func JWTFromQuery(c echo.Context) (string, error) {
	t := c.QueryParam("token")
	if t == "" {
		return "", echo.NewHTTPError(http.StatusUnauthorized, "token tidak ditemukan")
	}
	return t, nil
}
