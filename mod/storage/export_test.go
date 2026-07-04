package storage

import "context"

// // // // // // // // // //

// Test-only entry points run protected eviction paths without a caller-supplied protected set.
// Production keeps only the *ProtectedLocked variants that the runtime actually needs.

// //

func (obj *Obj) enforceHotBudgetLocked(ctx context.Context) error {
	return obj.enforceHotBudgetProtectedLocked(ctx, "", false)
}

// EnforceHotBudget brings the hot cache under the size limit via idle-first eviction.
func (obj *Obj) EnforceHotBudget(ctx context.Context) error {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return err
	}
	defer releaseFunc()

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()
	return obj.enforceHotBudgetLocked(ctx)
}

// //

func (obj *Obj) evictVersionsLocked(ctx context.Context, targetBytes uint64) error {
	return obj.evictVersionsProtectedLocked(ctx, targetBytes, protectedObjectsObj{})
}

// EvictVersions applies the version retention policy under writeMu.
// Without a byte target it prunes by policy only and does not evict down to a size target.
func (obj *Obj) EvictVersions(ctx context.Context) error {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return err
	}
	defer releaseFunc()

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()
	return obj.evictVersionsLocked(ctx, 0)
}
