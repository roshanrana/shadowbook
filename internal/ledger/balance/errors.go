package balance

import "errors"

// ErrInsufficientAvailable is returned when a hold exceeds available balance.
// httpapi maps it to 422.
var ErrInsufficientAvailable = errors.New("balance: insufficient available balance")
