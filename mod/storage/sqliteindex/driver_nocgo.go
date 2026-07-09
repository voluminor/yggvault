//go:build !cgo

package sqliteindex

import (
	"net/url"

	_ "modernc.org/sqlite"
)

// // // // // // // // // //

const sqliteDriverName = "sqlite"

// //

func sqliteDSN(pathToFile string) string {
	dsnObj := url.URL{Scheme: "file", Path: sqliteURIPath(pathToFile)}
	valueObj := url.Values{}
	valueObj.Add("_pragma", "busy_timeout(5000)")
	valueObj.Add("_pragma", "foreign_keys(ON)")
	valueObj.Add("_pragma", "journal_mode(WAL)")
	// WAL + synchronous=NORMAL fsyncs the WAL only at checkpoint, not on each commit (that is FULL).
	// No corruption risk, but power loss can roll back any transaction committed since the last
	// checkpoint; a commit is durable across a process crash, not across power loss.
	valueObj.Add("_pragma", "synchronous(NORMAL)")
	dsnObj.RawQuery = valueObj.Encode()
	return dsnObj.String()
}
