package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	dep "github.com/voluminor/yggvault/_generate"

	"gopkg.in/yaml.v3"
)

// // // // // // // // // //

const (
	packageName = "stcode"
	fileName    = "enums.go"

	cDefaultSourcePath = "yml/stcode/enums.yml"
)

//go:embed template.tmpl
var templateText string

//

// StringConstObj — deduplicated string constant of an enum value (shared across all types).
type StringConstObj struct {
	Name  string
	Value string
}

// EnumValueObj — a single enum value: constant name, reference to the string constant, and numeric code.
type EnumValueObj struct {
	ConstName      string
	ValueConstName string
	Code           int
}

// TemplateEnumObj — one enum type for the template: name, variable prefix, upper code bound, and values.
type TemplateEnumObj struct {
	TypeName  string
	VarPrefix string
	MaxCode   int
	Values    []EnumValueObj
}

// TemplateObj — root data for the enum generator template.
type TemplateObj struct {
	GenerationTime string
	PackageName    string
	StringConsts   []StringConstObj
	Enums          []TemplateEnumObj
}

//

func main() {
	sourcePath := flag.String("source", cDefaultSourcePath, "path to enums yml")
	flag.Parse()

	raw, err := os.ReadFile(*sourcePath)
	if err != nil {
		fmt.Println("Error reading enums config:", err)
		os.Exit(1)
	}

	data, err := buildTemplateObj(string(raw))
	if err != nil {
		fmt.Println("Error loading enums config:", err)
		os.Exit(1)
	}

	outPath := filepath.Join("target", packageName)
	_ = os.MkdirAll(outPath, os.ModePerm)
	outPath = filepath.Join(outPath, fileName)

	err = dep.WriteFileFromTemplate(outPath, templateText, data)
	if err != nil {
		fmt.Println("Error saving generated file:", err)
		os.Exit(1)
	}
}

func buildTemplateObj(configText string) (*TemplateObj, error) {
	var raw map[string][]string
	if err := yaml.Unmarshal([]byte(configText), &raw); err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(raw))
	for k := range raw {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	strConstByValue := map[string]string{}
	strConsts := make([]StringConstObj, 0)

	result := &TemplateObj{
		GenerationTime: time.Now().Format(time.RFC3339),
		PackageName:    packageName,
		Enums:          make([]TemplateEnumObj, 0, len(keys)),
	}

	for _, key := range keys {
		rawValues := raw[key]
		if len(rawValues) == 0 {
			continue
		}

		typeName := dep.GenGoName(key)
		item := TemplateEnumObj{
			TypeName:  typeName,
			VarPrefix: toLowerFirst(typeName),
			Values:    make([]EnumValueObj, 0, len(rawValues)),
		}

		for idx, value := range rawValues {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}

			constName, ok := strConstByValue[value]
			if !ok {
				constName = buildStringConstName(value)
				strConstByValue[value] = constName
				strConsts = append(strConsts, StringConstObj{Name: constName, Value: value})
			}

			item.Values = append(item.Values, EnumValueObj{
				ConstName:      typeName + dep.GenGoName(value),
				ValueConstName: constName,
				Code:           idx + 1,
			})
		}

		if len(item.Values) == 0 {
			continue
		}
		item.MaxCode = len(item.Values)
		result.Enums = append(result.Enums, item)
	}

	sort.Slice(strConsts, func(i, j int) bool {
		return strConsts[i].Name < strConsts[j].Name
	})
	result.StringConsts = strConsts

	return result, nil
}

func toLowerFirst(s string) string {
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && n == 0 {
		return s
	}
	return string(unicode.ToLower(r)) + s[n:]
}

func buildStringConstName(value string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(value))
	hexHash := hex.EncodeToString(h.Sum(nil))
	return "ecs" + hexHash[:12]
}
