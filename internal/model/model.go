// Package model holds the domain types shared by the engine, store and API.
package model

import (
	"time"

	"gomon/internal/protocol"
)

type Host struct {
	Name        string    `json:"name"`
	IP          string    `json:"ip"`
	Tags        string    `json:"tags"` // hosts.cfg style tags, e.g. "conn ssh http://x/"
	Description string    `json:"description"`
	Page        string    `json:"page"`
	OS          string    `json:"os"`
	Class       string    `json:"class"`
	Notes       string    `json:"notes"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Status struct {
	Host          string         `json:"host"`
	Test          string         `json:"test"`
	Color         protocol.Color `json:"-"`
	RealColor     protocol.Color `json:"-"` // color reported, before disable/purple handling
	Summary       string         `json:"summary"`
	Message       string         `json:"message,omitempty"`
	LastChange    time.Time      `json:"lastChange"`
	LastUpdate    time.Time      `json:"lastUpdate"`
	ValidUntil    time.Time      `json:"validUntil"`
	Sender        string         `json:"sender"`
	Transport     string         `json:"transport"`
	Group         string         `json:"group,omitempty"`
	Cookie        int64          `json:"cookie,omitempty"`
	AckUntil      *time.Time     `json:"ackUntil,omitempty"`
	AckMsg        string         `json:"ackMsg,omitempty"`
	AckBy         string         `json:"ackBy,omitempty"`
	DisabledUntil *time.Time     `json:"disabledUntil,omitempty"`
	DisableMsg    string         `json:"disableMsg,omitempty"`
}

// Acked reports whether the status is currently acknowledged.
func (s *Status) Acked(now time.Time) bool { return s.AckUntil != nil && s.AckUntil.After(now) }

// Disabled reports whether the status is currently disabled.
func (s *Status) Disabled(now time.Time) bool {
	return s.DisabledUntil != nil && s.DisabledUntil.After(now)
}

type HistoryEvent struct {
	ID        int64          `json:"id"`
	Host      string         `json:"host"`
	Test      string         `json:"test"`
	Time      time.Time      `json:"time"`
	Color     protocol.Color `json:"-"`
	PrevColor protocol.Color `json:"-"`
	Summary   string         `json:"summary"`
	Message   string         `json:"message,omitempty"`
}

type MetricSample struct {
	Host  string
	Test  string
	Name  string
	Time  time.Time
	Value float64
}

type ClientData struct {
	Host      string    `json:"host"`
	Collector string    `json:"collector"`
	Time      time.Time `json:"time"`
	OS        string    `json:"os"`
	Class     string    `json:"class"`
	Sender    string    `json:"sender"`
	Transport string    `json:"transport"`
	Data      string    `json:"data,omitempty"`
}

type SNMPTarget struct {
	ID              int64     `json:"id"`
	Host            string    `json:"host"`
	Address         string    `json:"address"`
	Port            int       `json:"port"`
	Version         string    `json:"version"` // 1, 2c, 3
	Community       string    `json:"community,omitempty"`
	V3              SNMPv3    `json:"v3"`
	IntervalSeconds int       `json:"intervalSeconds"`
	PollInterfaces  bool      `json:"pollInterfaces"`
	OIDs            []SNMPOID `json:"oids"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"createdAt"`
}

type SNMPv3 struct {
	Username      string `json:"username"`
	SecurityLevel string `json:"securityLevel"` // noAuthNoPriv, authNoPriv, authPriv
	AuthProtocol  string `json:"authProtocol"`  // MD5, SHA, SHA224, SHA256, SHA384, SHA512
	AuthPassword  string `json:"authPassword,omitempty"`
	PrivProtocol  string `json:"privProtocol"` // DES, AES, AES192, AES256
	PrivPassword  string `json:"privPassword,omitempty"`
	ContextName   string `json:"contextName,omitempty"`
}

// SNMPOID is a custom polled value with optional thresholds.
type SNMPOID struct {
	Name string `json:"name"`
	OID  string `json:"oid"`
	// Rate converts counters to per-second rates.
	Rate bool `json:"rate"`
	// Warn/Crit: thresholds (greater-or-equal unless Below is set).
	Warn  *float64 `json:"warn,omitempty"`
	Crit  *float64 `json:"crit,omitempty"`
	Below bool     `json:"below"`
	// Test is the column the value is reported under (default "snmp").
	Test string `json:"test,omitempty"`
}

type AlertChannel struct {
	ID      int64             `json:"id"`
	Name    string            `json:"name"`
	Type    string            `json:"type"` // webhook, slack, discord, email, ntfy
	Config  map[string]string `json:"config"`
	Enabled bool              `json:"enabled"`
}

type AlertRule struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	HostPattern    string  `json:"hostPattern"` // regex, empty = all
	TestPattern    string  `json:"testPattern"` // regex, empty = all
	ExcludeHosts   string  `json:"excludeHosts"`
	ExcludeTests   string  `json:"excludeTests"`
	MinColor       string  `json:"minColor"` // yellow, red, purple
	DelayMinutes   int     `json:"delayMinutes"`
	RepeatMinutes  int     `json:"repeatMinutes"`
	NotifyRecovery bool    `json:"notifyRecovery"`
	ChannelIDs     []int64 `json:"channelIds"`
	Enabled        bool    `json:"enabled"`
}
