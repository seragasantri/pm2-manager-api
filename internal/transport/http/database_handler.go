package httpx

import (
	"errors"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/tragasolusi/pm2-manager-api/internal/service"
	"github.com/tragasolusi/pm2-manager-api/internal/transport/http/response"
)

type DatabaseHandler struct {
	svc *service.DatabaseService
}

func NewDatabaseHandler(s *service.DatabaseService) *DatabaseHandler {
	return &DatabaseHandler{svc: s}
}

// Index GET /databases
func (h *DatabaseHandler) Index(c echo.Context) error {
	dbs, err := h.svc.GetDatabases(c.Request().Context())
	if err != nil {
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, dbs)
}

// Tables GET /databases/:dbName/tables
func (h *DatabaseHandler) Tables(c echo.Context) error {
	dbName := c.Param("dbName")
	if strings.TrimSpace(dbName) == "" {
		return response.ValidationError(c, map[string]string{"dbName": "dbName diperlukan"})
	}
	tables, err := h.svc.GetTables(c.Request().Context(), dbName)
	if err != nil {
		if errors.Is(err, service.ErrDBNameRequired) || errors.Is(err, service.ErrInvalidIdentifier) {
			return response.ValidationError(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, tables)
}

// Columns GET /databases/:dbName/tables/:table/columns
func (h *DatabaseHandler) Columns(c echo.Context) error {
	dbName := c.Param("dbName")
	table := c.Param("table")
	if strings.TrimSpace(dbName) == "" || strings.TrimSpace(table) == "" {
		return response.ValidationError(c, "dbName dan table diperlukan")
	}
	cols, err := h.svc.GetColumns(c.Request().Context(), dbName, table)
	if err != nil {
		if errors.Is(err, service.ErrDBNameRequired) || errors.Is(err, service.ErrTableNameRequired) || errors.Is(err, service.ErrInvalidIdentifier) {
			return response.ValidationError(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, cols)
}

// Data GET /databases/:dbName/tables/:table/data?page=&limit=
func (h *DatabaseHandler) Data(c echo.Context) error {
	dbName := c.Param("dbName")
	table := c.Param("table")
	if strings.TrimSpace(dbName) == "" || strings.TrimSpace(table) == "" {
		return response.ValidationError(c, "dbName dan table diperlukan")
	}
	page, _ := strconv.Atoi(c.QueryParam("page"))
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	res, err := h.svc.GetTableData(c.Request().Context(), dbName, table, page, limit)
	if err != nil {
		if errors.Is(err, service.ErrDBNameRequired) || errors.Is(err, service.ErrTableNameRequired) || errors.Is(err, service.ErrInvalidIdentifier) {
			return response.ValidationError(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, res)
}

type queryRequest struct {
	SQL string `json:"sql"`
}

// Query POST /databases/:dbName/query body {sql}
func (h *DatabaseHandler) Query(c echo.Context) error {
	dbName := c.Param("dbName")
	if strings.TrimSpace(dbName) == "" {
		return response.ValidationError(c, map[string]string{"dbName": "dbName diperlukan"})
	}
	var req queryRequest
	if err := c.Bind(&req); err != nil {
		return response.ValidationError(c, "body tidak valid")
	}
	if strings.TrimSpace(req.SQL) == "" {
		return response.ValidationError(c, map[string]string{"sql": "sql diperlukan"})
	}
	res, err := h.svc.QueryDatabase(c.Request().Context(), dbName, req.SQL)
	if err != nil {
		if errors.Is(err, service.ErrQueryBlocked) {
			return response.ValidationError(c, err.Error())
		}
		if errors.Is(err, service.ErrQueryRequired) || errors.Is(err, service.ErrDBNameRequired) || errors.Is(err, service.ErrInvalidIdentifier) {
			return response.ValidationError(c, err.Error())
		}
		return response.ServerError(c, err.Error())
	}
	return response.OK(c, res)
}
