package core

import "time"

// // // // // // // // // //

// FeedEventObj is a publish history event for Atom feeds: version, time, hashes, current release notes, and whether
// this is the first publication.
type FeedEventObj struct {
	Key          string
	Version      string
	EventTS      time.Time
	TreeHash     HashObj
	BodyHash     HashObj
	ReleaseNotes string
	FirstPublish bool
}
