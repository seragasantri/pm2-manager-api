package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/tragasolusi/pm2-manager-api/internal/config"
)

var (
	ErrDBNameRequired    = errors.New("nama database diperlukan")
	ErrTableNameRequired = errors.New("nama table diperlukan")
	ErrQueryRequired     = errors.New("sql diperlukan")
	ErrQueryBlocked      = errors.New("query tidak diizinkan: hanya SELECT/SHOW/DESCRIBE/EXPLAIN yang diperbolehkan")
	ErrInvalidIdentifier = errors.New("nama database/table tidak valid")
)

var identifierRe = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)
var blockedRe = regexp.MustCompile(`(?i)\b(drop|delete|update|insert|alter|create|truncate|grant|revoke|replace)\b`)

var allowedPrefixes = []string{"select", "show", "describe", "explain", "desc"}

var systemDBs = map[string]bool{
	"information_schema": true,
	"mysql":              true,
	"sys":                true,
	"performance_schema": true,
}

// DatabaseService provides read-only database introspection and querying.
type DatabaseService struct {
	db  *sql.DB
	cfg *config.AppConfig
}

func NewDatabaseService(db *sql.DB, cfg *config.AppConfig) *DatabaseService {
	return &DatabaseService{db: db, cfg: cfg}
}

// TableDataResult holds paginated table data.
type TableDataResult struct {
	Rows  []map[string]any `json:"rows"`
	Total int              `json:"total"`
	Page  int              `json:"page"`
	Limit int              `json:"limit"`
}

// QueryResult holds arbitrary query rows.
type QueryResult struct {
	Columns []string         `json:"columns"`
	Rows    []map[string]any `json:"rows"`
}

func validateIdentifier(name string) error {
	if name == "" {
		return ErrInvalidIdentifier
	}
	if !identifierRe.MatchString(name) {
		return ErrInvalidIdentifier
	}
	return nil
}

func rowsToMaps(rows *sql.Rows) ([]map[string]any, []string, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	result := make([]map[string]any, 0)
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		m := make(map[string]any, len(cols))
		for i, col := range cols {
			v := values[i]
			// handle []byte -> string
			if b, ok := v.([]byte); ok {
				m[col] = string(b)
			} else {
				m[col] = v
			}
		}
		result = append(result, m)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return result, cols, nil
}

// GetDatabases returns list of databases excluding system schemas.
func (s *DatabaseService) GetDatabases(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SHOW DATABASES")
	if err != nil {
		return nil, fmt.Errorf("show databases: %w", err)
	}
	defer rows.Close()
	var dbs []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if systemDBs[strings.ToLower(name)] {
			continue
		}
		dbs = append(dbs, name)
	}
	return dbs, rows.Err()
}

// GetTables returns tables in the given database.
func (s *DatabaseService) GetTables(ctx context.Context, dbName string) ([]string, error) {
	if strings.TrimSpace(dbName) == "" {
		return nil, ErrDBNameRequired
	}
	if err := validateIdentifier(dbName); err != nil {
		return nil, err
	}
	// SHOW TABLES FROM `dbName`
	query := fmt.Sprintf("SHOW TABLES FROM `%s`", dbName)
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("show tables: %w", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

// ColumnInfo describes a column from SHOW COLUMNS.
type ColumnInfo struct {
	Field   string  `json:"field"`
	Type    string  `json:"type"`
	Null    string  `json:"null"`
	Key     string  `json:"key"`
	Default *string `json:"default"`
	Extra   string  `json:"extra"`
}

// GetColumns returns columns for dbName.table.
func (s *DatabaseService) GetColumns(ctx context.Context, dbName, table string) ([]ColumnInfo, error) {
	if strings.TrimSpace(dbName) == "" {
		return nil, ErrDBNameRequired
	}
	if strings.TrimSpace(table) == "" {
		return nil, ErrTableNameRequired
	}
	if err := validateIdentifier(dbName); err != nil {
		return nil, err
	}
	if err := validateIdentifier(table); err != nil {
		return nil, err
	}
	query := fmt.Sprintf("SHOW COLUMNS FROM `%s`.`%s`", dbName, table)
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("show columns: %w", err)
	}
	defer rows.Close()
	var cols []ColumnInfo
	for rows.Next() {
		var c ColumnInfo
		var def sql.NullString
		if err := rows.Scan(&c.Field, &c.Type, &c.Null, &c.Key, &def, &c.Extra); err != nil {
			return nil, err
		}
		if def.Valid {
			c.Default = &def.String
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

// GetTableData returns paginated rows and total count.
func (s *DatabaseService) GetTableData(ctx context.Context, dbName, table string, page, limit int) (*TableDataResult, error) {
	if strings.TrimSpace(dbName) == "" {
		return nil, ErrDBNameRequired
	}
	if strings.TrimSpace(table) == "" {
		return nil, ErrTableNameRequired
	}
	if err := validateIdentifier(dbName); err != nil {
		return nil, err
	}
	if err := validateIdentifier(table); err != nil {
		return nil, err
	}
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	offset := (page - 1) * limit

	// total count
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM `%s`.`%s`", dbName, table)
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery).Scan(&total); err != nil {
		return nil, fmt.Errorf("count: %w", err)
	}

	dataQuery := fmt.Sprintf("SELECT * FROM `%s`.`%s` LIMIT %d OFFSET %d", dbName, table, limit, offset)
	rows, err := s.db.QueryContext(ctx, dataQuery)
	if err != nil {
		return nil, fmt.Errorf("select: %w", err)
	}
	defer rows.Close()

	maps, _, err := rowsToMaps(rows)
	if err != nil {
		return nil, err
	}
	if maps == nil {
		maps = []map[string]any{}
	}
	return &TableDataResult{Rows: maps, Total: total, Page: page, Limit: limit}, nil
}

// QueryDatabase executes a read-only query. Only SELECT/SHOW/DESCRIBE/EXPLAIN allowed.
func (s *DatabaseService) QueryDatabase(ctx context.Context, dbName, sqlQuery string) (*QueryResult, error) {
	if strings.TrimSpace(dbName) == "" {
		return nil, ErrDBNameRequired
	}
	if err := validateIdentifier(dbName); err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(sqlQuery)
	if trimmed == "" {
		return nil, ErrQueryRequired
	}
	lower := strings.ToLower(trimmed)

	// block dangerous keywords
	if blockedRe.MatchString(lower) {
		return nil, ErrQueryBlocked
	}

	// allow only specific prefixes
	allowed := false
	for _, p := range allowedPrefixes {
		if strings.HasPrefix(lower, p+" ") || lower == p || strings.HasPrefix(lower, p+"(") {
			allowed = true
			break
		}
		// also handle "show" without space variations already covered
		if strings.HasPrefix(lower, p) {
			// ensure prefix is at start; e.g. "select ..."
			// already checked above; this catches "show databases"
			if len(lower) >= len(p) && (len(lower) == len(p) || lower[len(p)] == ' ' || lower[len(p)] == ';') {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return nil, ErrQueryBlocked
	}

	// ponytail: no USE dbName switching; query runs on pool's default DB. Caller should qualify tables as dbName.table if needed. Upgrade: open dedicated *sql.Conn and exec "USE dbName".

	rows, err := s.db.QueryContext(ctx, trimmed)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	maps, cols, err := rowsToMaps(rows)
	if err != nil {
		return nil, err
	}
	if maps == nil {
		maps = []map[string]any{}
	}
	if cols == nil {
		cols = []string{}
	}
	return &QueryResult{Columns: cols, Rows: maps}, nil
}
