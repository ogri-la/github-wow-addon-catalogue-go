package main

// cooperation with Github's rate limits.
// see: https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api

import (
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// the Github REST API version sent with every API request.
// matches what Github serves when no version is requested.
const GITHUB_API_VERSION = "2022-11-28"

// pause after a primary rate limit when the reset time is missing, unreadable or past.
var PRIMARY_PAUSE_DEFAULT = 60 * time.Second

// `X-RateLimit-Reset` is truncated to the second, so a window really ends up
// to a second after it says. pauses and pacing aim this far past the reset.
var RESET_MARGIN = time.Second

// longest pause for any rate limit. the primary window is one hour.
var RATE_PAUSE_MAX = time.Hour

// first pause after a secondary rate limit without `Retry-After`.
// doubles with each consecutive secondary rate limit.
var SECONDARY_BACKOFF_MIN = 60 * time.Second

// longest pause after a secondary rate limit without `Retry-After`.
var SECONDARY_BACKOFF_MAX = 15 * time.Minute

// fraction of the `core` budget below which `core` requests are paced.
var CORE_RESERVE = 0.10

// a Github rate-limit resource, as named by the `X-RateLimit-Resource` header.
type RateResource = string

const (
	ResourceNone       RateResource = "" // not rate limited by Github's API
	ResourceCore       RateResource = "core"
	ResourceSearch     RateResource = "search"
	ResourceCodeSearch RateResource = "code_search"
)

// the kind of rate limit a response was refused with.
type LimitKind int

const (
	LimitNone LimitKind = iota
	LimitPrimary
	LimitSecondary
)

func (k LimitKind) String() string {
	switch k {
	case LimitPrimary:
		return "primary"
	case LimitSecondary:
		return "secondary"
	}
	return "none"
}

// returns the rate-limit resource a request to `u` counts against.
// the resource is needed before the request is sent, so it comes from the
// url and not from the `X-RateLimit-Resource` response header.
func resource_of(u *url.URL) RateResource {
	api, err := url.Parse(API_URL)
	if err != nil || u.Host != api.Host {
		return ResourceNone
	}
	if strings.HasPrefix(u.Path, "/search/code") {
		return ResourceCodeSearch
	}
	if strings.HasPrefix(u.Path, "/search/") {
		return ResourceSearch
	}
	return ResourceCore
}

// returns `true` when `raw_url` is a Github API url.
func is_api_url(raw_url string) bool {
	u, err := url.Parse(raw_url)
	return err == nil && resource_of(u) != ResourceNone
}

// returns the integer value of header `name`, and `false` when it is missing or unreadable.
func header_int(header http.Header, name string) (int64, bool) {
	val := header.Get(name)
	if val == "" {
		return 0, false
	}
	int_val, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0, false
	}
	return int_val, true
}

// returns the time in the `X-RateLimit-Reset` header, and `false` when it is missing or unreadable.
func header_reset(header http.Header) (time.Time, bool) {
	val, ok := header_int(header, "X-RateLimit-Reset")
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(val, 0), true
}

// returns the wait in the `Retry-After` header, and `false` when it is missing or unreadable.
// accepts both forms in RFC 9110: delay in seconds, or a HTTP date.
func header_retry_after(header http.Header, now time.Time) (time.Duration, bool) {
	val := header.Get("Retry-After")
	if val == "" {
		return 0, false
	}
	seconds, err := strconv.ParseInt(val, 10, 64)
	if err == nil {
		return max(time.Duration(seconds)*time.Second, 0), true
	}
	date, err := http.ParseTime(val)
	if err == nil {
		return max(date.Sub(now), 0), true
	}
	return 0, false
}

// classifies a response with `status` and `header` as a primary rate limit,
// a secondary rate limit or neither.
// a refusal with budget remaining, or with no rate-limit headers at all, is a
// secondary rate limit: the cautious reading of an ambiguous refusal.
func classify_limit(status int, header http.Header) LimitKind {
	if status != http.StatusForbidden && status != http.StatusTooManyRequests {
		return LimitNone
	}
	if header.Get("Retry-After") != "" {
		return LimitSecondary
	}
	remaining, ok := header_int(header, "X-RateLimit-Remaining")
	if ok && remaining == 0 {
		return LimitPrimary
	}
	return LimitSecondary
}

// returns how long to pause after a rate limit of `kind`, and `true` when the
// pause was capped at `RATE_PAUSE_MAX`.
// `backoff_level` counts the consecutive secondary rate limits before this one.
// a primary pause ends `RESET_MARGIN` after the reset. a reset less than
// `RESET_MARGIN` in the past is the current second, not a past one.
func limit_pause(kind LimitKind, header http.Header, now time.Time, backoff_level int) (time.Duration, bool) {
	var pause time.Duration
	switch kind {
	case LimitNone:
		return 0, false

	case LimitPrimary:
		pause = PRIMARY_PAUSE_DEFAULT
		reset, ok := header_reset(header)
		if ok {
			until := reset.Add(RESET_MARGIN).Sub(now)
			if until > 0 {
				pause = until
			}
		}

	case LimitSecondary:
		retry_after, ok := header_retry_after(header, now)
		if ok {
			pause = retry_after
		} else {
			pause = SECONDARY_BACKOFF_MAX
			if backoff_level < 16 { // beyond this the shift overflows and the cap applies anyway
				pause = min(SECONDARY_BACKOFF_MIN<<backoff_level, SECONDARY_BACKOFF_MAX)
			}
		}
	}

	if pause > RATE_PAUSE_MAX {
		return RATE_PAUSE_MAX, true
	}
	return pause, false
}

// returns the gap to leave before the next request to `resource`, given the
// budget `remaining` of `limit` until `reset`.
// spreads the remaining budget evenly over the time left until `RESET_MARGIN`
// after the reset, so the last request of a window is not refused for
// arriving early. search resources
// are always paced, `core` only once its budget falls below `CORE_RESERVE`.
// an unknown budget (`limit` of zero) is not paced.
func pace_interval(resource RateResource, remaining, limit int64, reset, now time.Time) time.Duration {
	if resource == ResourceNone || limit <= 0 {
		return 0
	}
	if resource == ResourceCore && float64(remaining) > float64(limit)*CORE_RESERVE {
		return 0
	}
	until_reset := reset.Add(RESET_MARGIN).Sub(now)
	if until_reset <= 0 {
		return 0
	}
	return until_reset / time.Duration(max(remaining, 1))
}

// returns the rate-limit headers of `header` as a log attribute group.
func rate_limit_attrs(header http.Header) slog.Attr {
	attrs := []any{}
	for _, name := range []string{"Retry-After", "X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset", "X-RateLimit-Resource"} {
		val := header.Get(name)
		if val != "" {
			attrs = append(attrs, slog.String(name, val))
		}
	}
	return slog.Group("rate-limit", attrs...)
}

// rate-limit state of a single resource.
type rate_state struct {
	paused_until  time.Time // no request before this time
	next_send     time.Time // earliest time pacing allows the next request
	remaining     int64     // budget left, from the most recent response
	limit         int64     // budget per window, from the most recent response. zero when unknown
	reset         time.Time // end of the current window, from the most recent response
	backoff_level int       // consecutive secondary rate limits
}

// the result of observing a response.
type limit_event struct {
	kind   LimitKind
	pause  time.Duration
	capped bool
}

// rate-limit state shared by every request.
// a map of resource to state under one mutex: a pause caused by one request
// must hold back every other request to the same resource, and no other.
type RateGate struct {
	mu    sync.Mutex
	state map[RateResource]*rate_state
	now   func() time.Time
	sleep func(time.Duration)
}

// returns a `RateGate` that uses the wall clock.
func new_rate_gate() *RateGate {
	return &RateGate{
		state: map[RateResource]*rate_state{},
		now:   time.Now,
		sleep: time.Sleep,
	}
}

// returns the state of `resource`, creating it when missing.
// the caller must hold `g.mu`.
func (g *RateGate) get(resource RateResource) *rate_state {
	s, ok := g.state[resource]
	if !ok {
		s = &rate_state{}
		g.state[resource] = s
	}
	return s
}

// blocks until a request to `resource` may be sent, then claims the next
// pacing slot. checks again after each sleep, because another request may
// have extended the pause in the meantime.
func (g *RateGate) wait(resource RateResource) {
	if resource == ResourceNone {
		return
	}
	for {
		g.mu.Lock()
		s := g.get(resource)
		now := g.now()
		ready_at := s.next_send
		if s.paused_until.After(ready_at) {
			ready_at = s.paused_until
		}
		if !now.Before(ready_at) {
			s.next_send = now.Add(pace_interval(resource, s.remaining, s.limit, s.reset, now))
			if s.remaining > 0 {
				s.remaining-- // this request spends one, before its response says so.
			}
			g.mu.Unlock()
			return
		}
		g.mu.Unlock()

		pause := ready_at.Sub(now)
		slog.Debug("waiting to send request", "resource", resource, "pause", pause)
		g.sleep(pause)
	}
}

// updates the state of `resource` from a response with `status` and `header`.
// a rate limit pauses the resource. a successful response ends any backoff.
// the returned pause is the pause for this response; the resource may stay
// paused for longer.
func (g *RateGate) observe(resource RateResource, status int, header http.Header) limit_event {
	if resource == ResourceNone {
		return limit_event{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	s := g.get(resource)
	now := g.now()

	if remaining, ok := header_int(header, "X-RateLimit-Remaining"); ok {
		s.remaining = remaining
	}
	if limit, ok := header_int(header, "X-RateLimit-Limit"); ok {
		s.limit = limit
	}
	if reset, ok := header_reset(header); ok {
		s.reset = reset
	}

	kind := classify_limit(status, header)
	if kind == LimitNone {
		if status >= 200 && status <= 299 {
			s.backoff_level = 0
		}
		return limit_event{}
	}

	// a refusal during a pause is from a request sent before the pause began.
	// it is part of the same refusal, so it neither escalates nor extends the
	// backoff. an explicit `Retry-After` is still honoured.
	if kind == LimitSecondary && now.Before(s.paused_until) && header.Get("Retry-After") == "" {
		return limit_event{kind: kind, pause: s.paused_until.Sub(now)}
	}

	pause, capped := limit_pause(kind, header, now, s.backoff_level)
	if kind == LimitSecondary {
		s.backoff_level++
	}
	paused_until := now.Add(pause)
	if paused_until.After(s.paused_until) {
		s.paused_until = paused_until
	}
	return limit_event{kind: kind, pause: pause, capped: capped}
}
