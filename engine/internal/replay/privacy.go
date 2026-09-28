package replay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/antifailure/antifailure/engine/internal/redact"
)

var artifactRedactor = redact.New()
var credentialName = regexp.MustCompile(`(?:authorization|cookie|password(?:hash)?|passwd|secret(?:accesskey)?|token|apikey|privatekey|connectionstring|credentials)$`)
var bearerValue = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[a-z0-9+/_.=~-]+`)
var urlInText = regexp.MustCompile(`https?://[^\s"'<>]+`)
var embeddedPair = regexp.MustCompile(`"((?:\\.|[^"\\])*)"\s*:\s*"((?:\\.|[^"\\])*)"`)

func sensitiveName(name string) bool {
	return credentialName.MatchString(strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(name)))
}

// SafePayload checks dynamic evidence, including decoded JSON strings. It
// refuses unsafe content rather than silently changing the experiment's bytes.
func SafePayload(body json.RawMessage) error {
	if len(body) == 0 {
		return nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("evidence is not JSON")
	}
	return safeValue(value, true)
}

func safeValue(value any, checkNames bool) error {
	return safeValueAt(value, checkNames, 0)
}

func safeValueAt(value any, checkNames bool, depth int) error {
	if depth > 64 {
		return fmt.Errorf("evidence exceeds the nesting limit")
	}
	switch v := value.(type) {
	case string:
		if artifactRedactor.String(v) != v || bearerValue.MatchString(v) {
			return fmt.Errorf("evidence contains a credential")
		}
		for _, pair := range embeddedPair.FindAllStringSubmatch(v, -1) {
			var key, item string
			if json.Unmarshal([]byte(`"`+pair[1]+`"`), &key) == nil && json.Unmarshal([]byte(`"`+pair[2]+`"`), &item) == nil && sensitiveName(key) && item != "[redacted]" && !strings.HasPrefix(item, "AF_FAKE_") {
				return fmt.Errorf("evidence text contains a credential field")
			}
		}
		trimmed := strings.TrimSpace(v)
		if len(trimmed) > 0 && strings.ContainsRune(`[{"`, rune(trimmed[0])) && json.Valid([]byte(trimmed)) {
			var nested any
			decoder := json.NewDecoder(strings.NewReader(trimmed))
			decoder.UseNumber()
			if decoder.Decode(&nested) == nil {
				if err := safeValueAt(nested, true, depth+1); err != nil {
					return err
				}
			}
		}
		for _, raw := range urlInText.FindAllString(v, -1) {
			u, err := url.Parse(raw)
			if err != nil {
				continue
			}
			for key, values := range u.Query() {
				if sensitiveName(key) || key == "sig" || key == "signature" || key == "X-Amz-Signature" {
					for _, item := range values {
						if item != "[redacted]" && !strings.HasPrefix(item, "AF_FAKE_") {
							return fmt.Errorf("evidence contains a credential URL")
						}
					}
				}
			}
		}
	case []any:
		for _, item := range v {
			if err := safeValueAt(item, checkNames, depth+1); err != nil {
				return err
			}
		}
	case map[string]any:
		for key, item := range v {
			if err := safeValueAt(key, false, depth+1); err != nil {
				return err
			}
			if checkNames && sensitiveName(key) && item != nil {
				allowed, ok := item.(string)
				if !ok || (allowed != "[redacted]" && !strings.HasPrefix(allowed, "AF_FAKE_")) {
					return fmt.Errorf("evidence contains a credential field")
				}
			}
			if err := safeValueAt(item, checkNames, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// safeArtifact is the final writer guard, also used for configuration blobs.
func safeArtifact(body []byte) error {
	if json.Valid(body) {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		return safeValue(value, false)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if err := safeValue(line, false); err != nil {
			return err
		}
	}
	return nil
}
