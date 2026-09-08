package db

import (
	"context"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5/pgconn"
)

var psql = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

// Execer is implemented by PostgreSQL pools and transactions.
type Execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}
