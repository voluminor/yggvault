package sqliteindex

import "embed"

// // // // // // // // // //

//go:embed migrations/*.sql
var migrationFSObj embed.FS
