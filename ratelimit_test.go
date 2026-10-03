package main

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// a fixed point in time for tests, whole seconds so `X-RateLimit-Reset` round trips.
var test_now = time.Unix(1791028689, 0)

// builds a `http.Header` from pairs of name and value.
func headers(pairs ...string) http.Header {
	header := http.Header{}
	for i := 0; i+1 < len(pairs); i += 2 {
		header.Set(pairs[i], pairs[i+1])
	}
	return header
}

// returns `t` as a `X-RateLimit-Reset` header value.
func reset_at(t time.Time) string {
	return strconv.FormatInt(t.Unix(), 10)
}

func Test_resource_of(t *testing.T) {
	cases := []struct {
		given    string
		expected RateResource
	}{
		{"https://api.github.com/search/code?q=foo", ResourceCodeSearch},
		{"https://api.github.com/search/repositories?q=foo", ResourceSearch},
		{"https://api.github.com/repos/foo/bar/releases", ResourceCore},
		{"https://github.com/foo/bar/releases/download/1.0/bar.zip", ResourceNone},
		{"https://objects.githubusercontent.com/foo", ResourceNone},
	}
	for _, c := range cases {
		given, err := url.Parse(c.given)
		require.NoError(t, err)
		actual := resource_of(given)
		assert.Equal(t, c.expected, actual, c.given)
	}
}

func Test_classify_limit(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		header   http.Header
		expected LimitKind
	}{
		{"success", 200, headers("X-RateLimit-Remaining", "0"), LimitNone},
		{"not found", 404, headers(), LimitNone},
		{"server error", 502, headers(), LimitNone},
		{"primary budget exhausted", 403, headers("X-RateLimit-Remaining", "0"), LimitPrimary},
		{"primary budget exhausted, 429", 429, headers("X-RateLimit-Remaining", "0"), LimitPrimary},
		{"retry-after present", 403, headers("Retry-After", "30"), LimitSecondary},
		{"retry-after beats exhausted budget", 403, headers("Retry-After", "30", "X-RateLimit-Remaining", "0"), LimitSecondary},
		{"refused with budget remaining", 403, headers("X-RateLimit-Remaining", "7"), LimitSecondary},
		{"refused with budget remaining, 429", 429, headers("X-RateLimit-Remaining", "7"), LimitSecondary},
		{"refused without rate-limit headers", 403, headers(), LimitSecondary},
	}
	for _, c := range cases {
		actual := classify_limit(c.status, c.header)
		assert.Equal(t, c.expected, actual, c.name)
	}
}

func Test_limit_pause(t *testing.T) {
	cases := []struct {
		name            string
		kind            LimitKind
		header          http.Header
		backoff_level   int
		expected        time.Duration
		expected_capped bool
	}{
		{"no limit", LimitNone, headers(), 0, 0, false},
		{"primary, reset in 40s", LimitPrimary, headers("X-RateLimit-Reset", reset_at(test_now.Add(40*time.Second))), 0, 41 * time.Second, false},
		{"primary, reset missing", LimitPrimary, headers(), 0, 60 * time.Second, false},
		{"primary, reset unreadable", LimitPrimary, headers("X-RateLimit-Reset", "soon"), 0, 60 * time.Second, false},
		{"primary, reset in the past", LimitPrimary, headers("X-RateLimit-Reset", reset_at(test_now.Add(-time.Minute))), 0, 60 * time.Second, false},
		{"primary, reset is the current second", LimitPrimary, headers("X-RateLimit-Reset", reset_at(test_now)), 0, time.Second, false},
		{"primary, reset beyond an hour", LimitPrimary, headers("X-RateLimit-Reset", reset_at(test_now.Add(3*time.Hour))), 0, time.Hour, true},
		{"secondary, retry-after seconds", LimitSecondary, headers("Retry-After", "90"), 3, 90 * time.Second, false},
		{"secondary, retry-after date", LimitSecondary, headers("Retry-After", test_now.Add(2*time.Minute).UTC().Format(http.TimeFormat)), 0, 2 * time.Minute, false},
		{"secondary, retry-after beyond an hour", LimitSecondary, headers("Retry-After", "7200"), 0, time.Hour, true},
		{"secondary, retry-after unreadable", LimitSecondary, headers("Retry-After", "soon"), 1, 120 * time.Second, false},
		{"secondary, first backoff", LimitSecondary, headers(), 0, 60 * time.Second, false},
		{"secondary, second backoff", LimitSecondary, headers(), 1, 120 * time.Second, false},
		{"secondary, third backoff", LimitSecondary, headers(), 2, 240 * time.Second, false},
		{"secondary, backoff capped", LimitSecondary, headers(), 5, 15 * time.Minute, false},
		{"secondary, backoff far past the cap", LimitSecondary, headers(), 100, 15 * time.Minute, false},
	}
	for _, c := range cases {
		actual, actual_capped := limit_pause(c.kind, c.header, test_now, c.backoff_level)
		assert.Equal(t, c.expected, actual, c.name)
		assert.Equal(t, c.expected_capped, actual_capped, c.name)
	}
}

func Test_pace_interval(t *testing.T) {
	in_a_minute := test_now.Add(time.Minute)
	in_an_hour := test_now.Add(time.Hour)
	cases := []struct {
		name      string
		resource  RateResource
		remaining int64
		limit     int64
		reset     time.Time
		expected  time.Duration
	}{
		{"code search budget", ResourceCodeSearch, 10, 10, in_a_minute, 6100 * time.Millisecond},
		{"search budget", ResourceSearch, 30, 30, in_a_minute, 2033333333 * time.Nanosecond},
		{"last request waits past the reset", ResourceCodeSearch, 1, 10, test_now.Add(5 * time.Second), 6 * time.Second},
		{"budget spent, wait past the reset", ResourceCodeSearch, 0, 10, in_a_minute, 61 * time.Second},
		{"plenty of core budget", ResourceCore, 4000, 5000, in_an_hour, 0},
		{"core budget below the reserve", ResourceCore, 277, 5000, in_an_hour, 13 * time.Second}, // 3601s / 277
		{"unknown budget", ResourceCodeSearch, 0, 0, time.Time{}, 0},
		{"reset in the past", ResourceCodeSearch, 5, 10, test_now.Add(-2 * time.Second), 0},
		{"not rate limited", ResourceNone, 0, 10, in_a_minute, 0},
	}
	for _, c := range cases {
		actual := pace_interval(c.resource, c.remaining, c.limit, c.reset, test_now)
		assert.Equal(t, c.expected, actual, c.name)
	}
}

func Test_rate_limit_attrs(t *testing.T) {
	given := headers("Retry-After", "60", "X-RateLimit-Resource", "code_search", "Content-Type", "application/json")
	actual := rate_limit_attrs(given)

	assert.Equal(t, "rate-limit", actual.Key)
	group := actual.Value.Group()
	require.Len(t, group, 2, "only rate-limit headers that are present")
	assert.Equal(t, "Retry-After", group[0].Key)
	assert.Equal(t, "X-RateLimit-Resource", group[1].Key)
}

// --- rate gate

// a clock whose sleeps advance time instantly.
type fake_clock struct {
	now   time.Time
	slept []time.Duration
}

// returns a `RateGate` driven by `clock`.
func fake_gate(clock *fake_clock) *RateGate {
	gate := new_rate_gate()
	gate.now = func() time.Time { return clock.now }
	gate.sleep = func(d time.Duration) {
		clock.slept = append(clock.slept, d)
		clock.now = clock.now.Add(d)
	}
	return gate
}

// sums every sleep `clock` has taken.
func total_slept(clock *fake_clock) time.Duration {
	total := time.Duration(0)
	for _, d := range clock.slept {
		total += d
	}
	return total
}

func Test_rate_gate__pause_holds_back_the_same_resource(t *testing.T) {
	clock := &fake_clock{now: test_now}
	gate := fake_gate(clock)

	event := gate.observe(ResourceCore, 403, headers("Retry-After", "30"))
	assert.Equal(t, LimitSecondary, event.kind)
	assert.Equal(t, 30*time.Second, event.pause)

	gate.wait(ResourceCore)
	assert.Equal(t, 30*time.Second, total_slept(clock), "a second caller waits for the pause to end")
}

func Test_rate_gate__pause_does_not_hold_back_other_resources(t *testing.T) {
	clock := &fake_clock{now: test_now}
	gate := fake_gate(clock)

	gate.observe(ResourceCodeSearch, 403, headers("Retry-After", "30"))
	gate.wait(ResourceCore)
	gate.wait(ResourceSearch)
	gate.wait(ResourceNone)
	assert.Empty(t, clock.slept)
}

func Test_rate_gate__primary_limit_waits_for_reset(t *testing.T) {
	clock := &fake_clock{now: test_now}
	gate := fake_gate(clock)

	given := headers("X-RateLimit-Remaining", "0", "X-RateLimit-Limit", "10", "X-RateLimit-Reset", reset_at(test_now.Add(40*time.Second)))
	event := gate.observe(ResourceCodeSearch, 403, given)
	assert.Equal(t, LimitPrimary, event.kind)

	gate.wait(ResourceCodeSearch)
	assert.GreaterOrEqual(t, total_slept(clock), 40*time.Second)
}

func Test_rate_gate__consecutive_secondary_limits_back_off(t *testing.T) {
	clock := &fake_clock{now: test_now}
	gate := fake_gate(clock)

	expected := []time.Duration{60 * time.Second, 120 * time.Second, 240 * time.Second}
	actual := []time.Duration{}
	for range expected {
		gate.wait(ResourceCodeSearch)
		actual = append(actual, gate.observe(ResourceCodeSearch, 403, headers()).pause)
	}
	assert.Equal(t, expected, actual)
}

func Test_rate_gate__success_resets_backoff(t *testing.T) {
	clock := &fake_clock{now: test_now}
	gate := fake_gate(clock)

	gate.observe(ResourceCodeSearch, 403, headers())
	gate.wait(ResourceCodeSearch)
	gate.observe(ResourceCodeSearch, 403, headers())
	gate.wait(ResourceCodeSearch)
	gate.observe(ResourceCodeSearch, 200, headers())
	gate.wait(ResourceCodeSearch)

	actual := gate.observe(ResourceCodeSearch, 403, headers()).pause
	assert.Equal(t, 60*time.Second, actual)
}

func Test_rate_gate__refusals_during_a_pause_do_not_escalate(t *testing.T) {
	// requests already in flight when the first refusal arrives are refused too.
	clock := &fake_clock{now: test_now}
	gate := fake_gate(clock)

	first := gate.observe(ResourceCodeSearch, 403, headers())
	clock.now = clock.now.Add(time.Second)
	second := gate.observe(ResourceCodeSearch, 403, headers())
	third := gate.observe(ResourceCodeSearch, 403, headers())

	assert.Equal(t, 60*time.Second, first.pause)
	assert.Equal(t, 59*time.Second, second.pause, "part of the same pause")
	assert.Equal(t, 59*time.Second, third.pause, "part of the same pause")

	gate.wait(ResourceCodeSearch)
	assert.Equal(t, 59*time.Second, total_slept(clock))

	// the next refusal is a new one, the second step of the backoff.
	actual := gate.observe(ResourceCodeSearch, 403, headers()).pause
	assert.Equal(t, 120*time.Second, actual)
}

func Test_rate_gate__paces_to_the_remaining_budget(t *testing.T) {
	clock := &fake_clock{now: test_now}
	gate := fake_gate(clock)

	gate.observe(ResourceCodeSearch, 200, headers("X-RateLimit-Remaining", "10", "X-RateLimit-Limit", "10", "X-RateLimit-Reset", reset_at(test_now.Add(time.Minute))))

	gate.wait(ResourceCodeSearch)
	assert.Empty(t, clock.slept, "the first request is not delayed")

	gate.wait(ResourceCodeSearch)
	expected := []time.Duration{6100 * time.Millisecond}
	assert.Equal(t, expected, clock.slept, "the next request waits its share of the window")
}

func Test_rate_gate__core_is_not_paced_with_budget_to_spare(t *testing.T) {
	clock := &fake_clock{now: test_now}
	gate := fake_gate(clock)

	gate.observe(ResourceCore, 200, headers("X-RateLimit-Remaining", "4000", "X-RateLimit-Limit", "5000", "X-RateLimit-Reset", reset_at(test_now.Add(time.Hour))))
	for range 10 {
		gate.wait(ResourceCore)
	}
	assert.Empty(t, clock.slept)
}

func Test_is_api_url(t *testing.T) {
	cases := []struct {
		given    string
		expected bool
	}{
		{"https://api.github.com/repos/foo/bar/releases", true},
		{"https://api.github.com/search/code?q=foo", true},
		{"https://github.com/foo/bar/releases/download/1.0/release.json", false},
		{"://not a url", false},
	}
	for _, c := range cases {
		actual := is_api_url(c.given)
		assert.Equal(t, c.expected, actual, c.given)
	}
}
