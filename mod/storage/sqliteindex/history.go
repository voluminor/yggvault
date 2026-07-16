package sqliteindex

import (
	"context"
	"fmt"
	"math"

	sq "github.com/Masterminds/squirrel"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

const cFirstPublishExpr = "NOT EXISTS (SELECT 1 FROM history_events p WHERE p.key = h.key AND p.version = h.version AND p.event_type = 'publish' AND p.id < h.id)"

func hashFromNullable(dataArr []byte) (core.HashObj, error) {
	if len(dataArr) == 0 {
		return core.HashObj{}, nil
	}
	if len(dataArr) != core.HashSize {
		return core.HashObj{}, fmt.Errorf("stored hash has invalid length %d", len(dataArr))
	}
	return core.HashFromBytes(dataArr)
}

// // // // // // // // // //

// ListPublishFeed returns newest-first publish events with release notes and the first-publish flag.
// An empty key means all keys; Atom feeds use this.
func (obj *Obj) ListPublishFeed(ctx context.Context, key string, limit int) ([]core.FeedEventObj, error) {
	if limit <= 0 {
		limit = 1
	}
	builderObj := obj.builderObj.
		Select("h.event_ts", "h.key", "h.version", "h.tree_hash", "h.body_hash", "COALESCE(v.release_notes, '')", cFirstPublishExpr).
		From("history_events h").
		LeftJoin("versions v ON v.key = h.key AND v.version = h.version").
		Where(sq.Eq{"h.event_type": "publish"})
	if key != "" {
		builderObj = builderObj.Where(sq.Eq{"h.key": key})
	}
	builderObj = builderObj.OrderBy("h.event_ts DESC", "h.id DESC").Limit(uint64(limit))

	rowsObj, err := querySQL(ctx, obj, nil, builderObj)
	if err != nil {
		return nil, err
	}
	defer rowsObj.Close()

	eventArr := make([]core.FeedEventObj, 0, limit)
	for rowsObj.Next() {
		var eventTS string
		var treeArr, bodyArr []byte
		var firstInt int
		eventObj := core.FeedEventObj{}
		if err = rowsObj.Scan(&eventTS, &eventObj.Key, &eventObj.Version, &treeArr, &bodyArr, &eventObj.ReleaseNotes, &firstInt); err != nil {
			return nil, err
		}
		if eventObj.EventTS, err = core.ParseTime(eventTS); err != nil {
			return nil, err
		}
		if eventObj.TreeHash, err = hashFromNullable(treeArr); err != nil {
			return nil, err
		}
		if eventObj.BodyHash, err = hashFromNullable(bodyArr); err != nil {
			return nil, err
		}
		eventObj.FirstPublish = firstInt != 0
		eventArr = append(eventArr, eventObj)
	}
	if err = rowsObj.Err(); err != nil {
		return nil, err
	}
	return eventArr, nil
}

// // // // // // // // // //

// PruneHistory deletes oldest events beyond maxEvents, bounding the append-only source for Atom.
// maxEvents==0 means unlimited; newest maxEvents by id are kept, and an underfilled set is unchanged.
func (obj *Obj) PruneHistory(ctx context.Context, maxEvents uint) (int64, error) {
	if maxEvents == 0 {
		return 0, nil
	}
	offsetValue := int64(maxEvents)
	if uint64(maxEvents) > math.MaxInt64 {
		offsetValue = math.MaxInt64
	}
	resultObj, err := execSQL(ctx, obj, nil, obj.builderObj.
		Delete("history_events").
		Where("id <= (SELECT id FROM history_events ORDER BY id DESC LIMIT 1 OFFSET ?)", offsetValue))
	if err != nil {
		return 0, err
	}
	return resultObj.RowsAffected()
}
