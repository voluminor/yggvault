package link

// // // // // // // // // //

// Obj carries entry host context: scheme, optional entry host, and nested route prefix.
type Obj struct {
	Scheme      string
	EntryHost   string
	RoutePrefix string
}

// // // // // // // // // //

// Base returns the mirror route base path: "/<prefix>" in nested mode, "" in root mode.
func (obj Obj) Base() string {
	if obj.RoutePrefix != "" {
		return "/" + obj.RoutePrefix
	}
	return ""
}

// Key builds a per-key web path with the nested prefix when nested mode is active.
func (obj Obj) Key(key string, suffix string) string {
	return obj.Base() + "/" + key + suffix
}

// Abs returns an absolute URL for the entry scheme, or a relative path when entry host is empty.
func (obj Obj) Abs(path string) string {
	if obj.EntryHost == "" {
		return path
	}
	return obj.Scheme + "://" + obj.EntryHost + path
}
