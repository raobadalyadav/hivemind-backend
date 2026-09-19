package meet

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

func sprintfBlocked(a, b string) string { return fmt.Sprintf(blockedEitherWay, a, b) }

func isBadUUID(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}
