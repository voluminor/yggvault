package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	dep "github.com/voluminor/yggvault/_generate"

	"gopkg.in/yaml.v3"
)

// // // // // // // // // //

const (
	packageName = "stcode"
	fileName    = "errors.go"

	cDefaultSourcePath = "yml/stcode/errors.yml"
)

var cPlaceholderRegex = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

//go:embed template.tmpl
var templateText string

//

type errorRawObj struct {
	Message string            `yaml:"message"`
	Fields  map[string]string `yaml:"fields"`
}

type configRawObj map[string]errorRawObj

// FieldTemplateObj — a single error field: Go type, expression/verb for the message format, and a zerolog snippet.
type FieldTemplateObj struct {
	YamlName       string
	FieldName      string
	ArgName        string
	GoType         string
	MessageArgExpr string
	MessageVerb    string
	ZLogSnippet    string
}

// ErrorTemplateObj — one error type for the template: name, compiled fmt message template, fields, and constructor.
type ErrorTemplateObj struct {
	Name            string
	TypeName        string
	MessageFmtConst string
	MessageArgs     string
	Fields          []FieldTemplateObj
	CtorArgs        string
	HasCauseField   bool
}

// TemplateObj — root data for the error generator template.
type TemplateObj struct {
	GenerationTime string
	PackageName    string
	Errors         []ErrorTemplateObj
}

//

func main() {
	sourcePath := flag.String("source", cDefaultSourcePath, "path to errors yml")
	flag.Parse()

	raw, err := os.ReadFile(*sourcePath)
	if err != nil {
		fmt.Println("Error reading errors config:", err)
		os.Exit(1)
	}

	data, err := buildTemplateObj(string(raw))
	if err != nil {
		fmt.Println("Error loading errors config:", err)
		os.Exit(1)
	}

	outPath := filepath.Join("target", packageName, fileName)
	err = dep.WriteFileFromTemplate(outPath, templateText, data)
	if err != nil {
		fmt.Println("Error saving generated file:", err)
		os.Exit(1)
	}
}

func buildTemplateObj(configText string) (*TemplateObj, error) {
	var raw configRawObj
	if err := yaml.Unmarshal([]byte(configText), &raw); err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := &TemplateObj{
		GenerationTime: time.Now().Format(time.RFC3339),
		PackageName:    packageName,
		Errors:         make([]ErrorTemplateObj, 0, len(keys)),
	}

	for _, key := range keys {
		obj := raw[key]

		fields, ctorArgs, hasCause, err := buildFields(obj.Fields)
		if err != nil {
			return nil, fmt.Errorf("%s.fields: %w", key, err)
		}

		fmtText, fmtArgs, err := compileMessage(obj.Message, fields)
		if err != nil {
			return nil, fmt.Errorf("%s.message: %w", key, err)
		}

		result.Errors = append(result.Errors, ErrorTemplateObj{
			Name:            key,
			TypeName:        "Err" + dep.GenGoName(key),
			MessageFmtConst: fmt.Sprintf("%q", fmtText),
			MessageArgs:     strings.Join(fmtArgs, ", "),
			Fields:          fields,
			CtorArgs:        ctorArgs,
			HasCauseField:   hasCause,
		})
	}

	return result, nil
}

func buildFields(rawFields map[string]string) ([]FieldTemplateObj, string, bool, error) {
	fieldNames := make([]string, 0, len(rawFields))
	for name := range rawFields {
		fieldNames = append(fieldNames, name)
	}
	sort.Strings(fieldNames)

	fields := make([]FieldTemplateObj, 0, len(fieldNames))
	args := make([]string, 0, len(fieldNames))
	hasCause := false

	for _, yamlName := range fieldNames {
		typeName := strings.TrimSpace(rawFields[yamlName])
		goType, msgArgExpr, msgVerb, zlogSnippet := resolveTypeAndSnippets(yamlName, typeName)

		fieldName := dep.GenGoName(yamlName)
		argName := toLowerFirst(fieldName)
		if argName == "" {
			return nil, "", false, fmt.Errorf("empty field name: %s", yamlName)
		}

		field := FieldTemplateObj{
			YamlName:       yamlName,
			FieldName:      fieldName,
			ArgName:        argName,
			GoType:         goType,
			MessageArgExpr: strings.ReplaceAll(msgArgExpr, "__FIELD__", "obj."+fieldName),
			MessageVerb:    msgVerb,
			ZLogSnippet:    strings.ReplaceAll(zlogSnippet, "__FIELD__", "obj."+fieldName),
		}
		fields = append(fields, field)
		args = append(args, fmt.Sprintf("%s %s", argName, goType))

		if fieldName == "Cause" && goType == "error" {
			hasCause = true
		}
	}

	return fields, strings.Join(args, ", "), hasCause, nil
}

func compileMessage(message string, fields []FieldTemplateObj) (string, []string, error) {
	byName := make(map[string]FieldTemplateObj, len(fields))
	for _, field := range fields {
		byName[field.YamlName] = field
	}

	matches := cPlaceholderRegex.FindAllStringSubmatchIndex(message, -1)
	if len(matches) == 0 {
		return message, nil, nil
	}

	args := make([]string, 0, len(matches))
	var out strings.Builder
	last := 0

	for _, idx := range matches {
		fullStart, fullEnd := idx[0], idx[1]
		nameStart, nameEnd := idx[2], idx[3]

		out.WriteString(message[last:fullStart])

		name := message[nameStart:nameEnd]
		field, ok := byName[name]
		if !ok {
			return "", nil, fmt.Errorf("placeholder {%s} is not declared in fields", name)
		}

		out.WriteString(field.MessageVerb)
		args = append(args, field.MessageArgExpr)
		last = fullEnd
	}

	out.WriteString(message[last:])
	return out.String(), args, nil
}

func resolveTypeAndSnippets(fieldName string, typeName string) (string, string, string, string) {
	if strings.HasPrefix(typeName, "enum.") {
		enumRef := strings.TrimPrefix(typeName, "enum.")
		typeGo := dep.GenGoName(enumRef) + "Type"
		msgArg := "__FIELD__.String()"
		msgVerb := "%s"
		snippet := fmt.Sprintf("event = event.Str(%q, __FIELD__.String())", fieldName)
		return typeGo, msgArg, msgVerb, snippet
	}

	goType := typeName
	msgArg := "__FIELD__"
	msgVerb := "%v"
	snippet := fmt.Sprintf("event = event.Str(%q, fmt.Sprintf(\"%%v\", __FIELD__))", fieldName)

	switch goType {
	case "string":
		msgVerb = "%s"
		snippet = fmt.Sprintf("event = event.Str(%q, __FIELD__)", fieldName)
	case "bool":
		msgVerb = "%t"
		snippet = fmt.Sprintf("event = event.Bool(%q, __FIELD__)", fieldName)
	case "int":
		msgVerb = "%d"
		snippet = fmt.Sprintf("event = event.Int(%q, __FIELD__)", fieldName)
	case "int64":
		msgVerb = "%d"
		snippet = fmt.Sprintf("event = event.Int64(%q, __FIELD__)", fieldName)
	case "uint":
		msgVerb = "%d"
		snippet = fmt.Sprintf("event = event.Uint(%q, __FIELD__)", fieldName)
	case "uint64":
		msgVerb = "%d"
		snippet = fmt.Sprintf("event = event.Uint64(%q, __FIELD__)", fieldName)
	case "uint32":
		msgVerb = "%d"
		snippet = fmt.Sprintf("event = event.Uint32(%q, __FIELD__)", fieldName)
	case "uint16":
		msgVerb = "%d"
		snippet = fmt.Sprintf("event = event.Uint16(%q, __FIELD__)", fieldName)
	case "uint8":
		msgVerb = "%d"
		snippet = fmt.Sprintf("event = event.Uint8(%q, __FIELD__)", fieldName)
	case "float64":
		msgVerb = "%f"
		snippet = fmt.Sprintf("event = event.Float64(%q, __FIELD__)", fieldName)
	case "error":
		msgArg = "func() string { if __FIELD__ == nil { return \"\" }; return __FIELD__.Error() }()"
		msgVerb = "%s"
		snippet = fmt.Sprintf("if __FIELD__ != nil { event = event.Str(%q, __FIELD__.Error()) }", fieldName)
	}

	return goType, msgArg, msgVerb, snippet
}

func toLowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = []rune(strings.ToLower(string(r[0])))[0]
	return string(r)
}
