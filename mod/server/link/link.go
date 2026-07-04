package link

// // // // // // // // // //

// Obj carries entry host context: scheme, optional entry host, and nested route prefix.
type Obj struct {
	Scheme      string
	EntryHost   string
	RoutePrefix string
}

// // // // // // // // // //

// Key builds a per-key web path with the nested prefix when nested mode is active.
func (obj Obj) Key(key string, suffix string) string {
	if obj.RoutePrefix != "" {
		return "/" + obj.RoutePrefix + "/" + key + suffix
	}
	return "/" + key + suffix
}

// Abs returns an absolute URL for the entry scheme, or a relative path when entry host is empty.
func (obj Obj) Abs(path string) string {
	if obj.EntryHost == "" {
		return path
	}
	return obj.Scheme + "://" + obj.EntryHost + path
}
