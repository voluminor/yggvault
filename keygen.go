package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"

	"github.com/voluminor/yggvault/mod/cli"
	"github.com/voluminor/yggvault/mod/mesh"
)

// // // // // // // // // //

const (
	// cKeyFileName — key file name in the current directory (this value is also what goes into yggdrasil.pem_key).
	cKeyFileName = "yggvault.pem"

	// cCmdMakeYggKey — command name for the machine envelope `command` and the text header (single source).
	cCmdMakeYggKey = "make-ygg-key"
)

// // // // // // // // // //

type keygenViewObj struct {
	Command  string `json:"command"`
	Ok       bool   `json:"ok"`
	FilePath string `json:"file_path"`
	Host     string `json:"host"`
	Force    bool   `json:"force"`
}

// // // // // // // // // //

func ensureKeyFileWritable(filePath string, force bool) error {
	if force {
		return nil
	}
	_, err := os.Stat(filePath)
	if err == nil {
		return fmt.Errorf("key file already exists: %s", filePath)
	}
	if os.IsNotExist(err) {
		return nil
	}
	return fmt.Errorf("stat key file %s: %w", filePath, err)
}

func reportKeygenError(jsonOutput bool, err error) error {
	if jsonOutput {
		renderErrorJSON(os.Stderr, cCmdMakeYggKey, err)
		return errAlreadyReported
	}
	return err
}

func renderKeygen(viewObj keygenViewObj, jsonOutput bool) error {
	if jsonOutput {
		return writeJSON(os.Stdout, viewObj)
	}
	bufObj := bufio.NewWriter(os.Stdout)
	fmt.Fprintln(bufObj, cCmdMakeYggKey)
	fmt.Fprintf(bufObj, "  file   %s\n", viewObj.FilePath)
	fmt.Fprintf(bufObj, "  host   %s\n", viewObj.Host)
	if viewObj.Force {
		fmt.Fprintln(bufObj, "  mode   force")
	}
	return bufObj.Flush()
}

// // // // // // // // // //

func runMakeYggKey(keygenObj cli.KeygenObj) error {
	dirPath, err := filepath.Abs(".")
	if err != nil {
		return reportKeygenError(keygenObj.JsonOutput, fmt.Errorf("resolve working directory: %w", err))
	}
	filePath := filepath.Join(dirPath, cKeyFileName)

	if err = ensureKeyFileWritable(filePath, keygenObj.Force); err != nil {
		return reportKeygenError(keygenObj.JsonOutput, err)
	}

	pemBytes, host, err := mesh.GenerateKey()
	if err != nil {
		return reportKeygenError(keygenObj.JsonOutput, fmt.Errorf("generate yggdrasil key: %w", err))
	}
	if err = mesh.WriteKeyFile(filePath, pemBytes); err != nil {
		return reportKeygenError(keygenObj.JsonOutput, err)
	}

	return renderKeygen(keygenViewObj{
		Command:  cCmdMakeYggKey,
		Ok:       true,
		FilePath: filePath,
		Host:     host,
		Force:    keygenObj.Force,
	}, keygenObj.JsonOutput)
}
