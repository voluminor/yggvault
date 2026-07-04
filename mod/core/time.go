package core

import "time"

// // // // // // // // // //

// FormatTime serializes a timestamp in the canonical UTC TimeFormat.
func FormatTime(ts time.Time) string {
	return ts.UTC().Format(TimeFormat)
}

// ParseTime parses a timestamp in TimeFormat.
func ParseTime(text string) (time.Time, error) {
	return time.Parse(TimeFormat, text)
}
