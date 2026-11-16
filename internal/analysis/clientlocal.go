package analysis

import (
	"strings"
)

// ClientLocal holds client-local.cfg: per host / class / OS sections that are
// returned to the client in response to its client message (e.g. which log
// files to watch).
type ClientLocal struct {
	sections map[string]string
}

// ParseClientLocal parses client-local.cfg ("[name]" sections).
func ParseClientLocal(content string) *ClientLocal {
	cl := &ClientLocal{sections: map[string]string{}}
	cur := ""
	var sb strings.Builder
	flush := func() {
		if cur != "" {
			cl.sections[strings.ToLower(cur)] = strings.TrimRight(sb.String(), "\n")
		}
		sb.Reset()
	}
	for _, ln := range strings.Split(content, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			flush()
			cur = t[1 : len(t)-1]
			continue
		}
		if cur != "" && !strings.HasPrefix(t, "#") {
			sb.WriteString(ln)
			sb.WriteByte('\n')
		}
	}
	flush()
	return cl
}

// For returns the section for a host, falling back to class then OS.
func (cl *ClientLocal) For(host, class, os string) string {
	if cl == nil {
		return ""
	}
	for _, k := range []string{host, class, os} {
		if k == "" {
			continue
		}
		if s, ok := cl.sections[strings.ToLower(k)]; ok {
			return s
		}
	}
	return ""
}

const DefaultClientLocalCfg = `# client-local.cfg - returned to xymon clients with their client message.
# Sections are a hostname, a client class, or an OS name.
# Example: have Linux clients send new lines of /var/log/messages
[linux]
log:/var/log/messages:10240

[sunos]
log:/var/adm/messages:10240
`
