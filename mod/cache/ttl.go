package cache

import "time"

// // // // // // // // // //

// SecondsToNextRescan returns time left until the next rescan. It is the shared source for metadata TTL and server
// `Cache-Control: max-age`; 0 means unknown, invalid, or already expired.
func SecondsToNextRescan(lastRescan time.Time, interval time.Duration, now time.Time) time.Duration {
	if lastRescan.IsZero() || interval <= 0 {
		return 0
	}
	remaining := lastRescan.Add(interval).Sub(now)
	if remaining < 0 {
		return 0
	}
	return remaining
}
