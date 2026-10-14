package arcadyan

import (
	"regexp"
	"strings"
)

// Editable settings are declared in cgi data files as
//   addCfg("<name>", "<numericID>", "<currentValue>")
// where the numeric ID is the apply_abstract.cgi form field name.
var (
	addCfgRe   = regexp.MustCompile(`addCfg\(\s*"([^"]*)"\s*,\s*"([^"]*)"\s*,\s*"((?:[^"\\]|\\.)*)"\s*\)`)
	cgisListRe = regexp.MustCompile(`var\s+CGIs\s*=\s*\[([^\]]*)\]`)
	cgisPushRe = regexp.MustCompile(`CGIs\.push\(\s*"([^"]+)"\s*\)`)
	strItemRe  = regexp.MustCompile(`"([^"]*)"`)
	varRe      = regexp.MustCompile(`var\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"((?:[^"\\]|\\.)*)"`)
)

// Field is one editable setting.
type Field struct {
	Name  string
	ID    string
	Value string
}

// Config is the ordered set of editable fields a page exposes.
type Config struct {
	Fields []Field
	byName map[string]int
}

// KV is a read-only variable from a cgi data file.
type KV struct{ Key, Val string }

func ParseConfig(js string) *Config {
	c := &Config{byName: map[string]int{}}
	c.merge(js)
	return c
}

func (c *Config) merge(js string) {
	if c.byName == nil {
		c.byName = map[string]int{}
	}
	for _, m := range addCfgRe.FindAllStringSubmatch(js, -1) {
		name := m[1]
		if _, dup := c.byName[name]; dup {
			continue
		}
		c.byName[name] = len(c.Fields)
		c.Fields = append(c.Fields, Field{Name: name, ID: m[2], Value: jsUnescape(m[3])})
	}
}

// Lookup returns the field with the given logical name.
func (c *Config) Lookup(name string) (Field, bool) {
	i, ok := c.byName[name]
	if !ok {
		return Field{}, false
	}
	return c.Fields[i], true
}

func parseVars(js string) []KV {
	var out []KV
	for _, m := range varRe.FindAllStringSubmatch(js, -1) {
		out = append(out, KV{m[1], jsUnescape(m[2])})
	}
	return out
}

func pageCGIs(html string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if m := cgisListRe.FindStringSubmatch(html); m != nil {
		for _, it := range strItemRe.FindAllStringSubmatch(m[1], -1) {
			add(it[1])
		}
	}
	for _, m := range cgisPushRe.FindAllStringSubmatch(html, -1) {
		add(m[1])
	}
	return out
}

func jsUnescape(s string) string {
	return strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\/`, `/`).Replace(s)
}
