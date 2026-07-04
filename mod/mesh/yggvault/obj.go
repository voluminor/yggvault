package yggvault

import (
	"github.com/voluminor/ratatoskr/mod/sigils"

	"github.com/voluminor/yggvault/target"
)

// // // // // // // // // //

// Obj stores sigil data: node build version, hash, and date.
type Obj struct {
	version string
	hash    string
	date    string
}

var _ sigils.Interface = (*Obj)(nil)

// New builds the sigil from generated target build metadata.
func New() *Obj {
	return &Obj{
		version: target.Version,
		hash:    target.Hash,
		date:    target.DateUpdate,
	}
}

// // // // // // // // // //

func (o *Obj) GetName() string { return Name() }

func (o *Obj) GetParams() []string { return Keys() }

// Params returns current sigil data as a NodeInfo fragment.
func (o *Obj) Params() map[string]any {
	return map[string]any{sigName: map[string]any{
		cKeyVersion: o.version,
		cKeyHash:    o.hash,
		cKeyDate:    o.date,
	}}
}

// SetParams writes sigil data into a NodeInfo copy without mutating the input.
func (o *Obj) SetParams(nodeInfo map[string]any) (map[string]any, error) {
	return sigils.MergeParams(nodeInfo, o.Params())
}

// ParseParams extracts this block from another NodeInfo and stores fields in the object.
func (o *Obj) ParseParams(nodeInfo map[string]any) map[string]any {
	parsed := ParseParams(nodeInfo)
	if block, ok := parsed[sigName].(map[string]any); ok {
		if v, ok := block[cKeyVersion].(string); ok {
			o.version = v
		}
		if v, ok := block[cKeyHash].(string); ok {
			o.hash = v
		}
		if v, ok := block[cKeyDate].(string); ok {
			o.date = v
		}
	}
	return parsed
}

func (o *Obj) Match(nodeInfo map[string]any) bool { return Match(nodeInfo) }

// Clone returns a deep copy of the sigil.
func (o *Obj) Clone() sigils.Interface {
	return &Obj{version: o.version, hash: o.hash, date: o.date}
}
