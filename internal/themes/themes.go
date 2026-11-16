// Package themes embeds the built-in UI themes. The theme stylesheets are
// taken from qui (https://github.com/autobrr/qui, GPL-2.0-or-later) so gomon
// looks and feels the same; see assets/NOTICE.
package themes

import (
	"embed"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
)

//go:embed assets/*.css
var assets embed.FS

type Theme struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Light       map[string]string `json:"light"`
	Dark        map[string]string `json:"dark"`
}

var (
	nameRe = regexp.MustCompile(`@name:\s*(.+?)\s*(?:\n|\*)`)
	descRe = regexp.MustCompile(`@description:\s*(.+?)\s*(?:\n|\*)`)
	declRe = regexp.MustCompile(`(--[A-Za-z0-9-]+)\s*:\s*([^;]+);`)
	varsRe = regexp.MustCompile(`@variations:\s*([^\n*]+)`)
)

var registry = load()

func All() []Theme { return registry }

func block(css, selector string) string {
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(selector) + `\s*\{`)
	loc := re.FindStringIndex(css)
	if loc == nil {
		return ""
	}
	end := strings.IndexByte(css[loc[1]:], '}')
	if end < 0 {
		return ""
	}
	return css[loc[1] : loc[1]+end]
}

const sansFallback = `ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif`
const monoFallback = `ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace`

func vars(b string) map[string]string {
	out := map[string]string{}
	for _, m := range declRe.FindAllStringSubmatch(b, -1) {
		name, val := m[1], strings.TrimSpace(m[2])
		if strings.Contains(val, "var("+name+")") {
			continue // self reference
		}
		switch name {
		case "--font-sans", "--font-serif":
			if !strings.Contains(val, "sans-serif") && !strings.Contains(val, "serif") {
				val += ", " + sansFallback
			}
		case "--font-mono":
			if !strings.Contains(val, "monospace") {
				val += ", " + monoFallback
			}
		}
		out[name] = val
	}
	return out
}

func load() []Theme {
	files, _ := fs.Glob(assets, "assets/*.css")
	var out []Theme
	for _, f := range files {
		b, err := assets.ReadFile(f)
		if err != nil {
			continue
		}
		css := string(b)
		t := Theme{ID: strings.TrimSuffix(path.Base(f), ".css")}
		if m := nameRe.FindStringSubmatch(css); m != nil {
			t.Name = m[1]
		}
		if m := descRe.FindStringSubmatch(css); m != nil {
			t.Description = m[1]
		}
		root := vars(block(css, ":root"))
		dark := vars(block(css, ".dark"))
		if _, ok := root["--variation-color"]; !ok {
			if m := varsRe.FindStringSubmatch(css); m != nil {
				first := strings.TrimSpace(strings.Split(m[1], ",")[0])
				if v, ok := root["--variation-"+first]; ok {
					root["--variation-color"] = v
				}
			}
		}
		merged := map[string]string{}
		for k, v := range root {
			merged[k] = v
		}
		for k, v := range dark {
			merged[k] = v
		}
		t.Light, t.Dark = root, merged
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID == "minimal" {
			return true
		}
		if out[j].ID == "minimal" {
			return false
		}
		return out[i].Name < out[j].Name
	})
	return out
}
