package sqliteindex

import (
	"context"
	"database/sql"
	"sync"
)

// // // // // // // // // //

const cStmtCacheCap = 512

// //

type stmtCacheObj struct {
	dbObj   *sql.DB
	muObj   sync.Mutex
	stmtMap map[string]*sql.Stmt
	capVal  int
}

func newStmtCache(dbObj *sql.DB, capVal int) *stmtCacheObj {
	return &stmtCacheObj{dbObj: dbObj, stmtMap: make(map[string]*sql.Stmt), capVal: capVal}
}

func (obj *stmtCacheObj) prepared(ctx context.Context, query string) (*sql.Stmt, bool) {
	obj.muObj.Lock()
	if stmtObj, ok := obj.stmtMap[query]; ok {
		obj.muObj.Unlock()
		return stmtObj, true
	}
	if len(obj.stmtMap) >= obj.capVal {
		obj.muObj.Unlock()
		return nil, false
	}
	obj.muObj.Unlock()

	stmtObj, err := obj.dbObj.PrepareContext(ctx, query)
	if err != nil {
		return nil, false
	}
	obj.muObj.Lock()
	defer obj.muObj.Unlock()
	if existing, ok := obj.stmtMap[query]; ok {
		_ = stmtObj.Close()
		return existing, true
	}
	if len(obj.stmtMap) >= obj.capVal {
		_ = stmtObj.Close()
		return nil, false
	}
	obj.stmtMap[query] = stmtObj
	return stmtObj, true
}

func (obj *stmtCacheObj) close() {
	obj.muObj.Lock()
	defer obj.muObj.Unlock()
	for _, stmtObj := range obj.stmtMap {
		_ = stmtObj.Close()
	}
	obj.stmtMap = nil
}
