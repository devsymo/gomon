package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"gomon/internal/alerts"
	"gomon/internal/hostscfg"
	"gomon/internal/model"
	"gomon/internal/snmp"
)

const secretMask = "********"

// ---- hosts.cfg import/export ----

func (a *API) exportHostsCfg(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="hosts.cfg"`)
	titles, _ := a.Store.PageTitles(r.Context())
	_, _ = w.Write([]byte(hostscfg.Format(a.Engine.Hosts(), titles)))
}

// pages returns page titles (from hosts.cfg "page NAME Title" lines).
func (a *API) pages(w http.ResponseWriter, r *http.Request) {
	titles, err := a.Store.PageTitles(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, titles)
}

func (a *API) importHostsCfg(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
		DryRun  bool   `json:"dryRun"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	parsed := hostscfg.Parse(body.Content)
	entries, warnings := parsed.Entries, parsed.Warnings
	created, updated := 0, 0
	if !body.DryRun {
		if err := a.Store.SetPageTitles(r.Context(), parsed.Titles); err != nil {
			warnings = append(warnings, "page titles: "+err.Error())
		}
	}
	for _, e := range entries {
		h, exists := a.Engine.Host(e.Name)
		if !exists {
			h = &model.Host{Name: e.Name}
			created++
		} else {
			updated++
		}
		h.IP, h.Tags, h.Page, h.Group = e.IP, e.Tags, e.Page, e.Group
		if body.DryRun {
			continue
		}
		if err := a.Engine.SaveHost(r.Context(), h); err != nil {
			warnings = append(warnings, e.Name+": "+err.Error())
		}
	}
	if !body.DryRun && len(entries) > 0 {
		go a.Net.RunOnce(context.Background())
	}
	if warnings == nil {
		warnings = []string{}
	}
	writeJSON(w, 200, map[string]any{"created": created, "updated": updated, "warnings": warnings, "dryRun": body.DryRun})
}

// ---- SNMP ----

func redactSNMP(list []*model.SNMPTarget) []*model.SNMPTarget {
	out := make([]*model.SNMPTarget, len(list))
	for i, t := range list {
		c := *t
		if c.Community != "" {
			c.Community = secretMask
		}
		if c.V3.AuthPassword != "" {
			c.V3.AuthPassword = secretMask
		}
		if c.V3.PrivPassword != "" {
			c.V3.PrivPassword = secretMask
		}
		out[i] = &c
	}
	return out
}

func (a *API) listSNMP(w http.ResponseWriter, r *http.Request) {
	list, err := a.Store.ListSNMPTargets(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if list == nil {
		list = []*model.SNMPTarget{}
	}
	writeJSON(w, 200, redactSNMP(list))
}

// mergeSecrets keeps stored secrets when the UI sends back the mask.
func mergeSecrets(t, old *model.SNMPTarget) {
	if old == nil {
		return
	}
	if t.Community == secretMask {
		t.Community = old.Community
	}
	if t.V3.AuthPassword == secretMask {
		t.V3.AuthPassword = old.V3.AuthPassword
	}
	if t.V3.PrivPassword == secretMask {
		t.V3.PrivPassword = old.V3.PrivPassword
	}
}

func normalizeSNMP(t *model.SNMPTarget) {
	t.Host = strings.ToLower(strings.TrimSpace(t.Host))
	t.Address = strings.TrimSpace(t.Address)
	if t.Address == "" {
		t.Address = t.Host
	}
	if t.Port == 0 {
		t.Port = 161
	}
	if t.Version == "" {
		t.Version = "2c"
	}
	if t.IntervalSeconds < 30 {
		t.IntervalSeconds = 300
	}
}

func (a *API) saveSNMP(w http.ResponseWriter, r *http.Request) {
	var t model.SNMPTarget
	if err := decode(r, &t); err != nil {
		writeErr(w, 400, err)
		return
	}
	if idStr := chi.URLParam(r, "id"); idStr != "" {
		t.ID, _ = strconv.ParseInt(idStr, 10, 64)
		old, err := a.Store.GetSNMPTarget(r.Context(), t.ID)
		if err != nil {
			writeErr(w, 404, "target not found")
			return
		}
		mergeSecrets(&t, old)
	}
	normalizeSNMP(&t)
	if t.Host == "" {
		writeErr(w, 400, "host is required")
		return
	}
	if _, ok := a.Engine.Host(t.Host); !ok {
		if err := a.Engine.SaveHost(r.Context(), &model.Host{Name: t.Host, IP: t.Address}); err != nil {
			writeErr(w, 400, err)
			return
		}
	}
	if err := a.Store.SaveSNMPTarget(r.Context(), &t); err != nil {
		writeErr(w, 500, err)
		return
	}
	if t.Enabled {
		go a.SNMP.PollAndReport(context.Background(), &t)
	}
	writeJSON(w, 200, redactSNMP([]*model.SNMPTarget{&t})[0])
}

func (a *API) deleteSNMP(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err := a.Store.DeleteSNMPTarget(r.Context(), id); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) testSNMP(w http.ResponseWriter, r *http.Request) {
	var t model.SNMPTarget
	if err := decode(r, &t); err != nil {
		writeErr(w, 400, err)
		return
	}
	if t.ID != 0 {
		if old, err := a.Store.GetSNMPTarget(r.Context(), t.ID); err == nil {
			mergeSecrets(&t, old)
		}
	}
	normalizeSNMP(&t)
	info, err := snmp.Test(&t)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "system": map[string]any{"name": info.Name, "descr": info.Descr,
		"location": info.Location, "uptime": info.Uptime.Truncate(time.Second).String()}})
}

func (a *API) pollSNMP(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	t, err := a.Store.GetSNMPTarget(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "target not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	a.SNMP.PollAndReport(ctx, t)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---- alerts ----

var secretKeys = map[string]bool{"password": true, "authorization": true, "token": true}

func redactChannels(list []*model.AlertChannel) []*model.AlertChannel {
	out := make([]*model.AlertChannel, len(list))
	for i, c := range list {
		cc := *c
		cc.Config = map[string]string{}
		for k, v := range c.Config {
			if secretKeys[k] && v != "" {
				v = secretMask
			}
			cc.Config[k] = v
		}
		out[i] = &cc
	}
	return out
}

func (a *API) listChannels(w http.ResponseWriter, r *http.Request) {
	list, err := a.Store.ListAlertChannels(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if list == nil {
		list = []*model.AlertChannel{}
	}
	writeJSON(w, 200, redactChannels(list))
}

func (a *API) findChannel(ctx context.Context, id int64) *model.AlertChannel {
	list, _ := a.Store.ListAlertChannels(ctx)
	for _, c := range list {
		if c.ID == id {
			return c
		}
	}
	return nil
}

func (a *API) saveChannel(w http.ResponseWriter, r *http.Request) {
	var c model.AlertChannel
	if err := decode(r, &c); err != nil {
		writeErr(w, 400, err)
		return
	}
	if idStr := chi.URLParam(r, "id"); idStr != "" {
		c.ID, _ = strconv.ParseInt(idStr, 10, 64)
		if old := a.findChannel(r.Context(), c.ID); old != nil {
			for k, v := range c.Config {
				if v == secretMask {
					c.Config[k] = old.Config[k]
				}
			}
		}
	}
	switch c.Type {
	case "webhook", "slack", "discord", "ntfy", "email":
	default:
		writeErr(w, 400, "unknown channel type")
		return
	}
	if strings.TrimSpace(c.Name) == "" {
		writeErr(w, 400, "name is required")
		return
	}
	if err := a.Store.SaveAlertChannel(r.Context(), &c); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, redactChannels([]*model.AlertChannel{&c})[0])
}

func (a *API) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err := a.Store.DeleteAlertChannel(r.Context(), id); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) testChannel(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	c := a.findChannel(r.Context(), id)
	if c == nil {
		writeErr(w, 404, "channel not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := a.Alerts.Send(ctx, c, alerts.TestNotification(a.Cfg.HTTP.BaseURL)); err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *API) listRules(w http.ResponseWriter, r *http.Request) {
	list, err := a.Store.ListAlertRules(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if list == nil {
		list = []*model.AlertRule{}
	}
	writeJSON(w, 200, list)
}

func (a *API) saveRule(w http.ResponseWriter, r *http.Request) {
	var rule model.AlertRule
	if err := decode(r, &rule); err != nil {
		writeErr(w, 400, err)
		return
	}
	if idStr := chi.URLParam(r, "id"); idStr != "" {
		rule.ID, _ = strconv.ParseInt(idStr, 10, 64)
	}
	if strings.TrimSpace(rule.Name) == "" {
		writeErr(w, 400, "name is required")
		return
	}
	if rule.MinColor == "" {
		rule.MinColor = "red"
	}
	if err := a.Store.SaveAlertRule(r.Context(), &rule); err != nil {
		writeErr(w, 500, err)
		return
	}
	a.Alerts.Kick()
	writeJSON(w, 200, rule)
}

func (a *API) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err := a.Store.DeleteAlertRule(r.Context(), id); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
