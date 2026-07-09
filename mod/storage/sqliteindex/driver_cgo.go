//go:build cgo

package sqliteindex

import (
	"net/url"

	_ "github.com/mattn/go-sqlite3"
)

// // // // // // // // // //

const sqliteDriverName = "sqlite3"

// //

func sqliteDSN(pathToFile string) string {
	dsnObj := url.URL{Scheme: "file", Path: sqliteURIPath(pathToFile)}
	valueObj := url.Values{}
	valueObj.Set("_busy_timeout", "5000")
	valueObj.Set("_foreign_keys", "on")
	valueObj.Set("_journal_mode", "WAL")
	// WAL + synchronous=NORMAL fsyncs the WAL only at checkpoint, not on each commit (that is FULL).
	// No corruption risk, but power loss can roll back any transaction committed since the last
	// checkpoint; a commit is durable across a process crash, not across power loss.
	valueObj.Set("_synchronous", "NORMAL")
	dsnObj.RawQuery = valueObj.Encode()
	return dsnObj.String()
}
