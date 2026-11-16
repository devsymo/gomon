// Package api is gomon's JSON HTTP API and the embedded web UI server.
package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"gomon/internal/alerts"
	"gomon/internal/auth"
	"gomon/internal/cfgfiles"
	"gomon/internal/config"
	"gomon/internal/engine"
	"gomon/internal/model"
	"gomon/internal/netmon"
	"gomon/internal/pki"
	"gomon/internal/protocol"
	"gomon/internal/snmp"
	"gomon/internal/store"
	"gomon/internal/themes"
	"gomon/internal/xymond"
)

type Deps struct {
	Cfg     *config.Config
	Store   *store.Store
	Engine  *engine.Engine
	Auth    *auth.Auth
	Cfgs    *cfgfiles.Manager
	Alerts  *alerts.Manager
	Net     *netmon.Monitor
	SNMP    *snmp.Poller
	CA      *pki.CA
	Xymond  *xymond.Server
	Web     fs.FS // built frontend (may be nil)
	Version string
	Log     *slog.Logger
	Started time.Time
}

type API struct{ Deps }

func New(d Deps) *API { return &API{d} }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err any) {
	msg := fmt.Sprint(err)
	if e, ok := err.(error); ok {
		msg = e.Error()
	}
	writeJSON(w, code, map[string]string{"error": msg})
}

func decode(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 20<<20)
	return json.NewDecoder(r.Body).Decode(v)
}

func (a *API) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RealIP, middleware.Recoverer, middleware.Compress(5, "application/json", "text/html", "text/css", "application/javascript"))
	r.Use(securityHeaders)

	r.Route("/api", func(r chi.Router) {
		r.Get("/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
		r.Get("/themes", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, themes.All()) })
		r.Get("/auth/info", a.Auth.Info)
		r.Post("/auth/setup", a.Auth.Setup)
		r.Post("/auth/login", a.Auth.Login)
		r.Get("/auth/oidc/login", a.Auth.OIDCLogin)
		r.Get("/auth/oidc/callback", a.Auth.OIDCCallback)
		r.Get("/tls/ca.pem", a.caPEM) // public: clients need it to trust the server

		r.Group(func(r chi.Router) {
			r.Use(a.Auth.Middleware)
			r.Get("/auth/me", a.Auth.Me)
			r.Post("/auth/logout", a.Auth.Logout)
			r.Post("/auth/password", a.Auth.ChangePassword)

			r.Get("/overview", a.overview)
			r.Get("/statuses", a.statuses)
			r.Get("/events", a.sse)
			r.Get("/system", a.system)

			r.Get("/hosts", a.listHosts)
			r.Post("/hosts", a.createHost)
			r.Get("/hosts/{host}", a.getHost)
			r.Put("/hosts/{host}", a.updateHost)
			r.Delete("/hosts/{host}", a.deleteHost)
			r.Post("/hosts/{host}/rename", a.renameHost)
			r.Get("/hosts/{host}/client", a.clientData)
			r.Get("/hosts/{host}/metrics", a.metrics)
			r.Post("/hosts/{host}/nettest", a.netTest)

			r.Get("/status/{host}/{test}", a.getStatus)
			r.Delete("/status/{host}/{test}", a.dropStatus)
			r.Post("/status/{host}/{test}/ack", a.ackStatus)
			r.Post("/status/{host}/{test}/disable", a.disableStatus)
			r.Post("/status/{host}/{test}/enable", a.enableStatus)

			r.Get("/history", a.history)
			r.Get("/history/{id}", a.historyEvent)

			r.Get("/config/{name}", a.getConfig)
			r.Put("/config/{name}", a.putConfig)
			r.Post("/config/{name}/validate", a.validateConfig)
			r.Get("/hostscfg", a.exportHostsCfg)
			r.Get("/pages", a.pages)
			r.Post("/hostscfg/import", a.importHostsCfg)

			r.Get("/snmp", a.listSNMP)
			r.Post("/snmp", a.saveSNMP)
			r.Put("/snmp/{id}", a.saveSNMP)
			r.Delete("/snmp/{id}", a.deleteSNMP)
			r.Post("/snmp/test", a.testSNMP)
			r.Post("/snmp/{id}/poll", a.pollSNMP)

			r.Get("/alerts/channels", a.listChannels)
			r.Post("/alerts/channels", a.saveChannel)
			r.Put("/alerts/channels/{id}", a.saveChannel)
			r.Delete("/alerts/channels/{id}", a.deleteChannel)
			r.Post("/alerts/channels/{id}/test", a.testChannel)
			r.Get("/alerts/rules", a.listRules)
			r.Post("/alerts/rules", a.saveRule)
			r.Put("/alerts/rules/{id}", a.saveRule)
			r.Delete("/alerts/rules/{id}", a.deleteRule)

			r.Get("/tls", a.tlsInfo)
			r.Post("/tls/issue", a.tlsIssue)
			r.Post("/tls/sign", a.tlsSign)

			r.Get("/apikeys", a.listAPIKeys)
			r.Post("/apikeys", a.createAPIKey)
			r.Delete("/apikeys/{id}", a.deleteAPIKey)
		})
		r.NotFound(func(w http.ResponseWriter, r *http.Request) { writeErr(w, 404, "not found") })
	})
	r.Handle("/*", a.spa())
	return r
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// spa serves the embedded frontend, falling back to index.html.
func (a *API) spa() http.Handler {
	if a.Web == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "web UI not built: run `make web` (or use the API)", http.StatusNotFound)
		})
	}
	files := http.FileServer(http.FS(a.Web))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" {
			if f, err := a.Web.Open(p); err == nil {
				st, _ := f.Stat()
				f.Close()
				if st != nil && !st.IsDir() {
					if strings.HasPrefix(p, "assets/") {
						w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					}
					files.ServeHTTP(w, r)
					return
				}
			}
		}
		idx, err := fs.ReadFile(a.Web, "index.html")
		if err != nil {
			http.Error(w, "index.html missing", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(idx)
	})
}

// ---- DTOs ----

type statusDTO struct {
	*model.Status
	Color    string `json:"color"`
	Acked    bool   `json:"acked"`
	Disabled bool   `json:"disabled"`
}

func toDTO(s *model.Status) statusDTO {
	now := time.Now()
	return statusDTO{Status: s, Color: s.Color.String(), Acked: s.Acked(now), Disabled: s.Disabled(now)}
}

func toDTOs(list []*model.Status) []statusDTO {
	out := make([]statusDTO, len(list))
	for i, s := range list {
		out[i] = toDTO(s)
	}
	return out
}

type historyDTO struct {
	*model.HistoryEvent
	Color     string `json:"color"`
	PrevColor string `json:"prevColor"`
}

type hostDTO struct {
	*model.Host
	LastSeen *time.Time        `json:"lastSeen,omitempty"`
	Color    string            `json:"color"`
	Tests    map[string]string `json:"tests"`
	Counts   map[string]int    `json:"counts"`
}

// ---- overview ----

func (a *API) overview(w http.ResponseWriter, r *http.Request) {
	sts := a.Engine.Statuses(nil, false)
	counts := map[string]int{"green": 0, "yellow": 0, "red": 0, "purple": 0, "blue": 0, "clear": 0}
	var problems []statusDTO
	hostWorst := map[string]protocol.Color{}
	for _, s := range sts {
		counts[s.Color.String()]++
		if c, ok := hostWorst[s.Host]; !ok || s.Color > c {
			hostWorst[s.Host] = s.Color
		}
		if s.Color.IsAlert() || s.Color == protocol.Purple {
			problems = append(problems, toDTO(s))
		}
	}
	hostCounts := map[string]int{"green": 0, "yellow": 0, "red": 0, "purple": 0, "blue": 0, "clear": 0}
	hosts := a.Engine.Hosts()
	for _, h := range hosts {
		c, ok := hostWorst[h.Name]
		if !ok {
			c = protocol.Clear
		}
		hostCounts[c.String()]++
	}
	recent, _ := a.Store.ListHistory(r.Context(), store.HistoryQuery{Limit: 15})
	writeJSON(w, 200, map[string]any{
		"statusCounts": counts,
		"hostCounts":   hostCounts,
		"hosts":        len(hosts),
		"tests":        len(sts),
		"problems":     problems,
		"recent":       historyDTOs(recent),
	})
}

func historyDTOs(list []*model.HistoryEvent) []historyDTO {
	out := make([]historyDTO, len(list))
	for i, e := range list {
		out[i] = historyDTO{HistoryEvent: e, Color: e.Color.String(), PrevColor: e.PrevColor.String()}
	}
	return out
}

func (a *API) statuses(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, toDTOs(a.Engine.Statuses(nil, false)))
}

func (a *API) system(w http.ResponseWriter, r *http.Request) {
	st := &a.Xymond.Stats
	writeJSON(w, 200, map[string]any{
		"version":     a.Version,
		"protocol":    xymond.Version,
		"uptime":      int(time.Since(a.Started).Seconds()),
		"plainListen": a.Cfg.Xymon.Listen,
		"tlsListen":   a.Cfg.Xymon.TLSListen,
		"oidc":        a.Cfg.OIDC.Enabled,
		"stats": map[string]int64{
			"connections":   st.Connections.Load(),
			"messages":      st.Messages.Load(),
			"tlsMessages":   st.TLSMessages.Load(),
			"rejected":      st.Rejected.Load(),
			"clientReports": st.ClientReports.Load(),
		},
	})
}

// sse streams status events to the UI.
func (a *API) sse(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	events, cancel := a.Engine.Subscribe()
	defer cancel()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	fmt.Fprint(w, ": connected\n\n")
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case ev, ok := <-events:
			if !ok {
				return
			}
			b, _ := json.Marshal(map[string]any{"type": ev.Type, "host": ev.Host, "test": ev.Test,
				"color": ev.Color.String(), "prevColor": ev.PrevColor.String(), "changed": ev.Changed})
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		}
	}
}

// ---- hosts ----

func (a *API) listHosts(w http.ResponseWriter, r *http.Request) {
	sts := a.Engine.Statuses(nil, false)
	byHost := map[string][]*model.Status{}
	for _, s := range sts {
		byHost[s.Host] = append(byHost[s.Host], s)
	}
	hosts := a.Engine.Hosts()
	out := make([]hostDTO, 0, len(hosts))
	for _, h := range hosts {
		d := hostDTO{Host: h, Color: "clear", Tests: map[string]string{}, Counts: map[string]int{}}
		worst := protocol.Color(-1)
		for _, s := range byHost[h.Name] {
			d.Tests[s.Test] = s.Color.String()
			d.Counts[s.Color.String()]++
			if d.LastSeen == nil || s.LastUpdate.After(*d.LastSeen) {
				t := s.LastUpdate
				d.LastSeen = &t
			}
			if s.Color > worst {
				worst = s.Color
			}
		}
		if worst >= 0 {
			d.Color = worst.String()
		}
		out = append(out, d)
	}
	writeJSON(w, 200, out)
}

func (a *API) getHost(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "host")
	h, ok := a.Engine.Host(name)
	if !ok {
		writeErr(w, 404, "host not found")
		return
	}
	sts := a.Engine.Statuses(func(s *model.Status) bool { return s.Host == name }, false)
	snmpTargets, _ := a.Store.ListSNMPTargets(r.Context())
	var mine []*model.SNMPTarget
	for _, t := range snmpTargets {
		if t.Host == name {
			mine = append(mine, t)
		}
	}
	writeJSON(w, 200, map[string]any{"host": h, "statuses": toDTOs(sts), "snmp": redactSNMP(mine)})
}

func (a *API) createHost(w http.ResponseWriter, r *http.Request) {
	var h model.Host
	if err := decode(r, &h); err != nil {
		writeErr(w, 400, err)
		return
	}
	h.Name = strings.ToLower(strings.TrimSpace(h.Name))
	if _, exists := a.Engine.Host(h.Name); exists {
		writeErr(w, 409, "host already exists")
		return
	}
	if err := a.Engine.SaveHost(r.Context(), &h); err != nil {
		writeErr(w, 400, err)
		return
	}
	go a.Net.TestHost(context.Background(), &h)
	writeJSON(w, 201, h)
}

func (a *API) updateHost(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "host")
	cur, ok := a.Engine.Host(name)
	if !ok {
		writeErr(w, 404, "host not found")
		return
	}
	var h model.Host
	if err := decode(r, &h); err != nil {
		writeErr(w, 400, err)
		return
	}
	h.Name = cur.Name
	if h.OS == "" {
		h.OS = cur.OS
	}
	if err := a.Engine.SaveHost(r.Context(), &h); err != nil {
		writeErr(w, 400, err)
		return
	}
	if h.Tags != cur.Tags || h.IP != cur.IP {
		go a.Net.TestHost(context.Background(), &h)
	}
	writeJSON(w, 200, h)
}

func (a *API) deleteHost(w http.ResponseWriter, r *http.Request) {
	if err := a.Engine.DeleteHost(r.Context(), chi.URLParam(r, "host")); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) renameHost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := decode(r, &body); err != nil || strings.TrimSpace(body.Name) == "" {
		writeErr(w, 400, "new name required")
		return
	}
	if _, exists := a.Engine.Host(strings.ToLower(body.Name)); exists {
		writeErr(w, 409, "a host with that name already exists")
		return
	}
	if err := a.Engine.RenameHost(r.Context(), chi.URLParam(r, "host"), body.Name); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) clientData(w http.ResponseWriter, r *http.Request) {
	data, err := a.Store.GetClientData(r.Context(), chi.URLParam(r, "host"))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	type sec struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	type out struct {
		*model.ClientData
		Sections []sec `json:"sections"`
	}
	res := make([]out, 0, len(data))
	for _, d := range data {
		o := out{ClientData: d}
		for _, s := range protocol.ParseSections(d.Data) {
			o.Sections = append(o.Sections, sec{s.Name, s.Content})
		}
		d.Data = ""
		res = append(res, o)
	}
	writeJSON(w, 200, res)
}

var ranges = map[string]time.Duration{"1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour, "90d": 90 * 24 * time.Hour, "365d": 365 * 24 * time.Hour}

func (a *API) metrics(w http.ResponseWriter, r *http.Request) {
	host := chi.URLParam(r, "host")
	rng := ranges[r.URL.Query().Get("range")]
	if rng == 0 {
		rng = 24 * time.Hour
	}
	to := time.Now()
	from := to.Add(-rng)
	step := rng / 300
	if step < time.Minute {
		step = time.Minute
	}
	series, err := a.Store.QueryMetrics(r.Context(), host, r.URL.Query().Get("test"), from, to, step)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if series == nil {
		series = []*store.MetricSeries{}
	}
	writeJSON(w, 200, map[string]any{"from": from.UnixMilli(), "to": to.UnixMilli(), "step": int(step.Seconds()), "series": series})
}

func (a *API) netTest(w http.ResponseWriter, r *http.Request) {
	h, ok := a.Engine.Host(chi.URLParam(r, "host"))
	if !ok {
		writeErr(w, 404, "host not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	a.Net.TestHost(ctx, h)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---- statuses ----

func (a *API) getStatus(w http.ResponseWriter, r *http.Request) {
	st, ok := a.Engine.Status(chi.URLParam(r, "host"), chi.URLParam(r, "test"))
	if !ok {
		writeErr(w, 404, "status not found")
		return
	}
	writeJSON(w, 200, toDTO(st))
}

func (a *API) dropStatus(w http.ResponseWriter, r *http.Request) {
	if err := a.Engine.Drop(r.Context(), chi.URLParam(r, "host"), chi.URLParam(r, "test")); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

type actionBody struct {
	Minutes  int    `json:"minutes"`
	Message  string `json:"message"`
	AllTests bool   `json:"allTests"`
}

func (a *API) ackStatus(w http.ResponseWriter, r *http.Request) {
	var b actionBody
	if err := decode(r, &b); err != nil || b.Minutes <= 0 {
		writeErr(w, 400, "minutes must be > 0")
		return
	}
	u := auth.UserFrom(r.Context())
	n, err := a.Engine.Ack(r.Context(), 0, chi.URLParam(r, "host"), chi.URLParam(r, "test"), time.Duration(b.Minutes)*time.Minute, b.Message, u.Username)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]int{"acked": n})
}

func (a *API) disableStatus(w http.ResponseWriter, r *http.Request) {
	var b actionBody
	if err := decode(r, &b); err != nil || b.Minutes == 0 {
		writeErr(w, 400, "minutes required (-1 = until OK)")
		return
	}
	test := chi.URLParam(r, "test")
	if b.AllTests {
		test = "*"
	}
	dur := time.Duration(b.Minutes) * time.Minute
	if b.Minutes < 0 {
		dur = -1
	}
	u := auth.UserFrom(r.Context())
	msg := strings.TrimSpace(b.Message)
	if msg == "" {
		msg = "Disabled"
	}
	n, err := a.Engine.Disable(r.Context(), chi.URLParam(r, "host"), test, dur, msg+" (by "+u.Username+")")
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]int{"disabled": n})
}

func (a *API) enableStatus(w http.ResponseWriter, r *http.Request) {
	var b actionBody
	_ = decode(r, &b)
	test := chi.URLParam(r, "test")
	if b.AllTests {
		test = "*"
	}
	n, err := a.Engine.Enable(r.Context(), chi.URLParam(r, "host"), test)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]int{"enabled": n})
}

// ---- history ----

func (a *API) history(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	hq := store.HistoryQuery{Host: q.Get("host"), Test: q.Get("test")}
	hq.Before, _ = strconv.ParseInt(q.Get("before"), 10, 64)
	hq.Limit, _ = strconv.Atoi(q.Get("limit"))
	if cs := q.Get("colors"); cs != "" {
		for _, c := range strings.Split(cs, ",") {
			if col, ok := protocol.ParseColor(c); ok {
				hq.Colors = append(hq.Colors, col)
			}
		}
	}
	list, err := a.Store.ListHistory(r.Context(), hq)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, historyDTOs(list))
}

func (a *API) historyEvent(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	ev, err := a.Store.GetHistoryEvent(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, 404, "not found")
		return
	} else if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, historyDTO{HistoryEvent: ev, Color: ev.Color.String(), PrevColor: ev.PrevColor.String()})
}

// ---- config files ----

func validName(n string) bool {
	for _, v := range cfgfiles.Names {
		if v == n {
			return true
		}
	}
	return false
}

func (a *API) getConfig(w http.ResponseWriter, r *http.Request) {
	n := chi.URLParam(r, "name")
	if !validName(n) {
		writeErr(w, 404, "unknown config file")
		return
	}
	c, _ := a.Cfgs.Get(n)
	writeJSON(w, 200, map[string]string{"name": n, "content": c})
}

func (a *API) putConfig(w http.ResponseWriter, r *http.Request) {
	n := chi.URLParam(r, "name")
	var body struct {
		Content string `json:"content"`
	}
	if !validName(n) || decode(r, &body) != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	if err := a.Cfgs.Put(r.Context(), n, body.Content); err != nil {
		writeErr(w, 422, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) validateConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
	}
	if decode(r, &body) != nil {
		writeErr(w, 400, "invalid request")
		return
	}
	if err := cfgfiles.Validate(chi.URLParam(r, "name"), body.Content); err != nil {
		writeJSON(w, 200, map[string]any{"valid": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"valid": true})
}

// ---- TLS / agents ----

func (a *API) caPEM(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="gomon-ca.pem"`)
	_, _ = w.Write(a.CA.PEM)
}

func (a *API) tlsInfo(w http.ResponseWriter, r *http.Request) {
	sum := sha256.Sum256(a.CA.Cert.Raw)
	writeJSON(w, 200, map[string]any{
		"caPem":             string(a.CA.PEM),
		"caSubject":         a.CA.Cert.Subject.CommonName,
		"caExpires":         a.CA.Cert.NotAfter,
		"caFingerprint":     strings.ToUpper(hex.EncodeToString(sum[:])),
		"plainListen":       a.Cfg.Xymon.Listen,
		"tlsListen":         a.Cfg.Xymon.TLSListen,
		"requireClientCert": a.Cfg.Xymon.RequireClientCert,
		"customServerCert":  a.Cfg.Xymon.TLSCert != "",
		"serverNames":       pki.DefaultServerNames(a.Cfg.Xymon.TLSServerNames),
	})
}

func (a *API) tlsIssue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Hostname string `json:"hostname"`
		Days     int    `json:"days"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	cert, key, err := a.CA.IssueClient(body.Hostname, body.Days)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	a.Log.Info("issued client certificate", "host", body.Hostname, "by", auth.UserFrom(r.Context()).Username)
	writeJSON(w, 200, map[string]string{"cert": string(cert), "key": string(key), "ca": string(a.CA.PEM)})
}

func (a *API) tlsSign(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CSR      string `json:"csr"`
		Hostname string `json:"hostname"`
		Days     int    `json:"days"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	cert, err := a.CA.SignCSR([]byte(body.CSR), body.Hostname, body.Days)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]string{"cert": string(cert), "ca": string(a.CA.PEM)})
}

// ---- API keys ----

func (a *API) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := a.Store.ListAPIKeys(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if keys == nil {
		keys = []store.APIKey{}
	}
	writeJSON(w, 200, keys)
}

func (a *API) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := decode(r, &body); err != nil || strings.TrimSpace(body.Name) == "" {
		writeErr(w, 400, "name required")
		return
	}
	id, key, err := a.Auth.NewAPIKey(r.Context(), body.Name)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "name": body.Name, "key": key})
}

func (a *API) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err := a.Store.DeleteAPIKey(r.Context(), id); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
