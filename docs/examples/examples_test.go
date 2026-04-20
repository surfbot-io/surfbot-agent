// Package examples carries the tiny guard tests that keep the curl-
// pasteable JSON fences in docs/examples/*.md valid as the surrounding
// code evolves. The test parses every ```json fenced block out of every
// example and asserts it round-trips through encoding/json.
package examples

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// jsonFence matches a ```json ... ``` block. MultilineDotall so (?s)
// isn't needed — we match up to the closing fence non-greedily.
var jsonFence = regexp.MustCompile("(?s)```json\\s*\\n(.*?)\\n```")

func TestExamples_JSONBlocksParse(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read docs/examples: %v", err)
	}
	var seen int
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(".", e.Name()))
			if err != nil {
				t.Fatalf("read %s: %v", e.Name(), err)
			}
			matches := jsonFence.FindAllStringSubmatch(string(raw), -1)
			if len(matches) == 0 {
				t.Fatalf("%s: no ```json blocks", e.Name())
			}
			for i, m := range matches {
				body := m[1]
				// Replace TEMPLATE_ID / TARGET_ID placeholders with a
				// valid JSON string so the block parses.
				body = strings.NewReplacer(
					"TEMPLATE_ID", "11111111-1111-1111-1111-111111111111",
					"TARGET_ID", "22222222-2222-2222-2222-222222222222",
				).Replace(body)
				var v any
				if err := json.Unmarshal([]byte(body), &v); err != nil {
					t.Errorf("%s block %d: invalid JSON: %v\n---\n%s\n---", e.Name(), i, err, body)
				}
				seen++
			}
		})
	}
	if seen < 4 {
		t.Fatalf("expected at least 4 JSON blocks across examples, saw %d", seen)
	}
}
