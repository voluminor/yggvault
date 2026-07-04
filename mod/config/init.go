package config

import (
	stcfg "github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

// New parses a config file through stconf, applying defaults and schema min/max before business validation.
func New(pathToFile string) (*stcfg.ConfigObj, error) {
	stc, err := stcfg.ParseFile(pathToFile)
	if err != nil {
		return nil, err
	}
	if err = validate(stc); err != nil {
		return nil, err
	}
	return stc, nil
}
