package serr

import "errors"

// // // // // // // // // //

var (
	// ErrNotFound maps missing resources to 404.
	ErrNotFound = errors.New("not found")

	// ErrBadInput maps invalid client input to 400.
	ErrBadInput = errors.New("bad input")

	// ErrUnavailable maps temporary resource failures to 503.
	ErrUnavailable = errors.New("unavailable")

	// ErrRangeNotSatisfiable maps unsatisfiable range requests to 416.
	ErrRangeNotSatisfiable = errors.New("range not satisfiable")
)
