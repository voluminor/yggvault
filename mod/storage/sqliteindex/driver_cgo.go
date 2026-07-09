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
	// NORMAL under WAL fsyncs WAL on commit, but skips FULL's extra checkpoint sync.
	// There is no corruption risk; power loss can only lose the latest committed transaction.
	valueObj.Set("_synchronous", "NORMAL")
	dsnObj.RawQuery = valueObj.Encode()
	return dsnObj.String()
}
