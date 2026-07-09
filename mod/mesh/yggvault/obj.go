package yggvault

import (
	"fmt"

	"github.com/voluminor/ratatoskr/mod/sigils"
)

// // // // // // // // // //

// Obj stores sigil data: node build version, hash, and date.
type Obj struct {
	version string
	hash    string
	date    string
}

var _ sigils.Interface = (*Obj)(nil)

// New validates and creates the publisher sigil with node build metadata.
func New(version string, hash string, date string) (*Obj, error) {
	if err := validateFields(version, hash, date); err != nil {
		return nil, err
	}
	return &Obj{version: version, hash: hash, date: date}, nil
}

// // // // // // // // // //

func validateField(name string, value string, required bool) error {
	if required && value == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	if len(value) > cMaxValueBytes {
		return fmt.Errorf("%s exceeds %d bytes", name, cMaxValueBytes)
	}
	return nil
}

func validateFields(version string, hash string, date string) error {
	if err := validateField(cKeyVersion, version, true); err != nil {
		return err
	}
	if err := validateField(cKeyHash, hash, false); err != nil {
		return err
	}
	if err := validateField(cKeyDate, date, false); err != nil {
		return err
	}
	return nil
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
