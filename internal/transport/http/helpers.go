package httpx

import (
	"errors"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

// parseID extracts a numeric :id param and reports a validation error.
func parseID(c echo.Context, key string) (int64, error) {
	v := c.Param(key)
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, errors.New(key + " tidak valid")
	}
	return id, nil
}

// mapError maps service-layer errors to HTTP responses.
func mapError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, errInvalidInput) || errors.Is(err, errValidation):
		return response.ValidationError(c, err.Error())
	case errors.Is(err, errUnauthorized):
		return response.Unauthorized(c, err.Error())
	case errors.Is(err, errForbidden):
		return response.Forbidden(c, err.Error())
	case errors.Is(err, errNotFound):
		return response.NotFound(c, err.Error())
	default:
		return response.ServerError(c, err.Error())
	}
}

var (
	errInvalidInput = errors.New("invalid input")
	errValidation   = errors.New("validation failed")
	errUnauthorized = errors.New("unauthorized")
	errForbidden    = errors.New("forbidden")
	errNotFound     = errors.New("not found")
)
