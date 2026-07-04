package telemetry

import (
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/attribute"
)

// // // // // // // // // //

func groupForScope(scopeName string) Group {
	if rest, ok := strings.CutPrefix(scopeName, cScopePrefix); ok {
		if group := Group(rest); knownGroupSetObj[group] {
			return group
		}
	}
	return GroupCore
}

// //

func sanitizeName(name string) string {
	var builderObj strings.Builder
	builderObj.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == ':':
			builderObj.WriteRune(r)
		default:
			builderObj.WriteByte('_')
		}
	}
	return builderObj.String()
}

func escapeLabelValue(value string) string {
	if !strings.ContainsAny(value, "\\\"\n") {
		return value
	}
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(value)
}

func escapeHelp(text string) string {
	if !strings.ContainsAny(text, "\\\n") {
		return text
	}
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(text)
}

func labelPairs(attrs attribute.Set) []string {
	if attrs.Len() == 0 {
		return nil
	}
	pairs := make([]string, 0, attrs.Len())
	iterObj := attrs.Iter()
	for iterObj.Next() {
		kv := iterObj.Attribute()
		pairs = append(pairs, sanitizeName(string(kv.Key))+`="`+escapeLabelValue(kv.Value.String())+`"`)
	}
	return pairs
}

func renderLabels(pairs []string) string {
	if len(pairs) == 0 {
		return ""
	}
	return "{" + strings.Join(pairs, ",") + "}"
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

// formatNumber renders int64 exactly in base 10, and float64 via formatFloat.
func formatNumber[N int64 | float64](value N) string {
	if intValue, ok := any(value).(int64); ok {
		return strconv.FormatInt(intValue, 10)
	}
	return formatFloat(float64(value))
}
