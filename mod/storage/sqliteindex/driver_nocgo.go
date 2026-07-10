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
	valueObj.Add("_pragma", "synchronous(NORMAL)")
	dsnObj.RawQuery = valueObj.Encode()
	return dsnObj.String()
}
