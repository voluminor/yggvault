package cache

import "context"

// // // // // // // // // //

// BuildGateObj limits detached builds shared by the byte-cache and the obj-cache.
type BuildGateObj struct {
	sem chan struct{}
}

// // // // // // // // // //

func NewBuildGate(maxParallel uint) *BuildGateObj {
	if maxParallel == 0 {
		return nil
	}
	return &BuildGateObj{sem: make(chan struct{}, maxParallel)}
}

func (obj *BuildGateObj) Acquire(ctx context.Context) error {
	if obj == nil {
		return nil
	}
	select {
	case obj.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (obj *BuildGateObj) Release() {
	if obj == nil {
		return
	}
	<-obj.sem
}
