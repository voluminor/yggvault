package util

// // // // // // // // // //

// GenMapObj is a two-generation string-keyed map: hits promote entries from the previous
// generation, and the whole current generation rotates out once it reaches cap. The effective
// ceiling is about 2*cap entries without per-entry eviction bookkeeping.
// It is not safe for concurrent use; callers hold their own locks.
type GenMapObj[V any] struct {
	curMap  map[string]V
	prevMap map[string]V
	capVal  int
}

// NewGenMap builds a map with the given per-generation capacity (minimum 1).
func NewGenMap[V any](capVal int) *GenMapObj[V] {
	if capVal < 1 {
		capVal = 1
	}
	return &GenMapObj[V]{
		curMap:  make(map[string]V, capVal),
		prevMap: map[string]V{},
		capVal:  capVal,
	}
}

// Get returns the stored value, promoting hits from the previous generation.
func (obj *GenMapObj[V]) Get(key string) (V, bool) {
	if value, ok := obj.curMap[key]; ok {
		return value, true
	}
	if value, ok := obj.prevMap[key]; ok {
		obj.curMap[key] = value
		return value, true
	}
	var zeroValue V
	return zeroValue, false
}

// Delete removes the key from both generations.
func (obj *GenMapObj[V]) Delete(key string) {
	delete(obj.curMap, key)
	delete(obj.prevMap, key)
}

// Put stores the value, rotating generations when the current one is full.
func (obj *GenMapObj[V]) Put(key string, value V) {
	if len(obj.curMap) >= obj.capVal {
		obj.prevMap = obj.curMap
		obj.curMap = make(map[string]V, obj.capVal)
	}
	obj.curMap[key] = value
}
