package yggvault

// // // // // // // // // //

// Version returns the node build version from the sigil.
func (o *Obj) Version() string { return o.version }

// Hash returns the node build hash from the sigil.
func (o *Obj) Hash() string { return o.hash }

// Date returns the node build date from the sigil.
func (o *Obj) Date() string { return o.date }
