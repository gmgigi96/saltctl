package main

import (
	"regexp"
	"strings"
)

// The firmware's cgi data files declare each editable setting as
//   addCfg("<name>", "<numericID>", "<currentValue>")
// The numeric ID is the form field name expected by apply_abstract.cgi.
var addCfgRe = regexp.MustCompile(`addCfg\(\s*"([^"]*)"\s*,\s*"([^"]*)"\s*,\s*"((?:[^"\\]|\\.)*)"\s*\)`)

type cfgField struct {
	Name  string
	ID    string
	Value string
}

// Config is the ordered set of editable fields a page exposes.
type Config struct {
	Fields []cfgField
	byName map[string]int
}

func parseConfig(js string) *Config {
	c := &Config{byName: map[string]int{}}
	for _, m := range addCfgRe.FindAllStringSubmatch(js, -1) {
		name := m[1]
		if _, dup := c.byName[name]; dup {
			continue
		}
		c.byName[name] = len(c.Fields)
		c.Fields = append(c.Fields, cfgField{Name: name, ID: m[2], Value: jsUnescape(m[3])})
	}
	return c
}

func (c *Config) lookup(name string) (cfgField, bool) {
	i, ok := c.byName[name]
	if !ok {
		return cfgField{}, false
	}
	return c.Fields[i], true
}

func jsUnescape(s string) string {
	r := strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\/`, `/`)
	return r.Replace(s)
}
