package sqliteindex

import (
	"context"
	"database/sql"
	"errors"

	sq "github.com/Masterminds/squirrel"
)

// // // // // // // // // //

// GetGlobal returns a value from the globals key/value table; a missing key returns ("", false, nil).
func (obj *Obj) GetGlobal(ctx context.Context, name string) (string, bool, error) {
	rowObj, err := queryRowSQL(ctx, obj, nil, obj.builderObj.
		Select("value").
		From("globals").
		Where(sq.Eq{"name": name}))
	if err != nil {
		return "", false, err
	}
	var valueText string
	switch scanErr := rowObj.Scan(&valueText); {
	case errors.Is(scanErr, sql.ErrNoRows):
		return "", false, nil
	case scanErr != nil:
		return "", false, scanErr
	}
	return valueText, true, nil
}

// SetGlobal upserts a value into the globals key/value table.
func (obj *Obj) SetGlobal(ctx context.Context, name string, value string) error {
	_, err := execSQL(ctx, obj, nil, obj.builderObj.
		Insert("globals").
		Columns("name", "value").
		Values(name, value).
		Suffix("ON CONFLICT(name) DO UPDATE SET value=excluded.value"))
	return err
}
