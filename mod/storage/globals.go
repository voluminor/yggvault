package storage

import "context"

// // // // // // // // // //

// GetGlobal returns a value from the durable globals key/value table; a missing key returns ("", false, nil).
func (obj *Obj) GetGlobal(ctx context.Context, name string) (string, bool, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return "", false, err
	}
	defer releaseFunc()

	return obj.indexObj.GetGlobal(ctx, name)
}

// SetGlobal upserts a value into the durable globals key/value table under writeMu.
func (obj *Obj) SetGlobal(ctx context.Context, name string, value string) error {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return err
	}
	defer releaseFunc()

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()

	return obj.indexObj.SetGlobal(ctx, name, value)
}
