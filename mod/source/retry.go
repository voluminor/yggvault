package source

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

// // // // // // // // // //

type permanentErrObj struct {
	err error
}

// Error returns the wrapped error message.
func (e permanentErrObj) Error() string { return e.err.Error() }

// Unwrap exposes the wrapped error for errors.Is and errors.As.
func (e permanentErrObj) Unwrap() error { return e.err }

// //

type retryObj struct {
	maxAttempts    uint8
	backoffInitial time.Duration
	backoffMax     time.Duration
	jitterPercent  uint16
}

// // // // // // // // // //

func permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentErrObj{err: err}
}

func isPermanent(err error) bool {
	var p permanentErrObj
	return errors.As(err, &p)
}

// // // // // // // // // //

func (r retryObj) do(ctx context.Context, op func(ctx context.Context) error) error {
	var last error
	for attempt := uint8(1); ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		last = op(ctx)
		if last == nil {
			return nil
		}
		if isPermanent(last) {
			return last
		}
		if attempt >= r.maxAttempts {
			return last
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.backoff(attempt)):
		}
	}
}

func (r retryObj) backoff(attempt uint8) time.Duration {
	base := r.backoffInitial
	for i := uint8(1); i < attempt && base < r.backoffMax; i++ {
		base *= 2
	}
	if base > r.backoffMax {
		base = r.backoffMax
	}
	if base <= 0 {
		return 0
	}

	if r.jitterPercent == 0 {
		return base
	}
	delta := base * time.Duration(r.jitterPercent) / 100
	if delta <= 0 {
		return base
	}
	return base - delta + time.Duration(rand.Int64N(int64(2*delta)+1))
}
