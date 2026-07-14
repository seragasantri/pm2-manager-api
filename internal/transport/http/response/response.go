package response

import "github.com/labstack/echo/v4"

// Envelope is the standard JSON shape returned to clients.
type Envelope struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    any    `json:"data"`
	Errors  any    `json:"errors,omitempty"`
}

func OK(c echo.Context, data any, msg ...string) error {
	m := "Success"
	if len(msg) > 0 {
		m = msg[0]
	}
	return c.JSON(200, Envelope{Success: true, Message: m, Data: data})
}

func Created(c echo.Context, data any, msg ...string) error {
	m := "Created successfully"
	if len(msg) > 0 {
		m = msg[0]
	}
	return c.JSON(201, Envelope{Success: true, Message: m, Data: data})
}

func Error(c echo.Context, status int, msg string, errs ...any) error {
	var e any
	if len(errs) > 0 {
		e = errs[0]
	}
	return c.JSON(status, Envelope{Success: false, Message: msg, Errors: e})
}

func Unauthorized(c echo.Context, msg string) error {
	return Error(c, 401, msg)
}

func Forbidden(c echo.Context, msg string) error {
	return Error(c, 403, msg)
}

func NotFound(c echo.Context, msg string) error {
	return Error(c, 404, msg)
}

func ValidationError(c echo.Context, errs any) error {
	return Error(c, 422, "Validation failed", errs)
}

func ServerError(c echo.Context, msg string) error {
	return Error(c, 500, msg)
}