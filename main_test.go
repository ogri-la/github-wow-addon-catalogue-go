package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func Test_parse_toc_filename(t *testing.T) {
	cases := map[string][]string{
		"Foo.toc":          {"Foo", ""},
		"Foo-mainline.toc": {"Foo", MainlineFlavor},
		"Foo_mainline.toc": {"Foo", MainlineFlavor},
		//"Foo.mainline.toc": {"Foo", MainlineFlavor}, // todo: this *should* work

		"Foo-classic.toc": {"Foo", VanillaFlavor},
		"Foo-bcc.toc":     {"Foo", TBCFlavor},
		"Foo-wrath.toc":   {"Foo", WrathFlavor},
		"Foo-cata.toc":    {"Foo", CataFlavor},

		"Foo-1.0.toc":          {"Foo-1.0", ""},
		"Foo-1.0-mainline.toc": {"Foo-1.0", MainlineFlavor},
		"Foo-1.0_mainline.toc": {"Foo-1.0", MainlineFlavor},
		//"Foo-1.0.mainline.toc": {"Foo-1.0", MainlineFlavor}, // todo: this *should* work

		"Foo-1.0-classic.toc": {"Foo-1.0", VanillaFlavor},
		"Foo-1.0-vanilla.toc": {"Foo-1.0", VanillaFlavor},
		"Array_Vanilla.toc":   {"Array", VanillaFlavor},

		"Foo-1.0-bcc.toc": {"Foo-1.0", TBCFlavor},
		"Foo-1.0-tbc.toc": {"Foo-1.0", TBCFlavor},

		"Foo-1.0-wrath.toc":  {"Foo-1.0", WrathFlavor},
		"Foo-1.0-wotlk.toc":  {"Foo-1.0", WrathFlavor},
		"Foo-1.0-wotlkc.toc": {"Foo-1.0", WrathFlavor},

		"Foo-1.0-cata.toc": {"Foo-1.0", CataFlavor},

		// bit of an edgecase, filename has a space in it.
		"Loot-A-Rang Matic Reforged.toc": {"Loot-A-Rang Matic Reforged", ""},
	}
	for given, expected := range cases {
		actual_filename, actual_flavor := parse_toc_filename(given)
		expected_filename, expected_flavor := expected[0], expected[1]
		assert.Equal(t, expected_filename, actual_filename)
		assert.Equal(t, expected_flavor, actual_flavor)
	}
}

func Test_is_toc_file(t *testing.T) {
	cases := map[string]bool{
		"":                         false,
		"-_!@#$%^&*(":              false, // gibberish
		"Foo":                      false, // top level file
		"Foo/Bar":                  false, // no '.toc'
		"Foo/Bar.toc":              false, // 'Foo' must match 'Bar'
		"Foo/Foo/Foo.toc":          false, // nested
		"Foo/foo.toc":              true,  // 'Foo' matches 'foo' ignoring case.
		"Foo/fOo.toc":              true,  // 'Foo' matches 'fOo' ignoring case.
		"Foo/Foo.toc":              true,
		"Foo/Foo-wrath.toc":        true,
		"Foo/Foo_wrath.toc":        true,
		"Foo-1.0/Foo-1.0.toc":      true,
		"Foo-1.0/Foo-1.0-cata.toc": true,
		"Foo-1.0/Foo-1.0_cata.toc": true,

		// case insensitive
		"Foo/Foo-WRATH.toc": true,
		"Foo/Foo-WrAtH.ToC": true,

		// addon name contain itself contains a game track (Classic)
		"JadeUI-Classic/JadeUI-Classic.toc": true,
		"JadeUI-Vanilla/JadeUI-Vanilla.toc": true,
		"JadeUI-Vanilla/JadeUI-Classic.toc": true, // works because flavors coerced from aliases

		// space in name
		"Loot-A-Rang Matic Reforged/Loot-A-Rang Matic Reforged.toc": true,

		// apostrophe
		"Ranoth's utility/Ranoth's utility.toc":       true,
		"RanothsUtility-v1.3.19/Ranoth's utility.toc": false, // version information in folder name.

		// exclamation mark
		"!!!GarbageProtector/!!!GarbageProtector-Mainline.toc": true,
	}
	for given, expected := range cases {
		assert.Equal(t, expected, is_toc_file(given), given)
	}
}

func Test_is_excluded(t *testing.T) {
	cases := map[string]bool{
		"":           false, // matches nothing
		"foo":        false,
		"foo/":       true, // matches 'foo/'
		"foo/bar":    true, // matches 'foo/'
		"foo/barbaz": true, // matches 'foo/'
	}
	blacklist := map[string]bool{
		"foo/": true,
	}
	var filter *regexp.Regexp
	for repo_fullname, expected := range cases {
		_, actual := is_excluded(blacklist, filter, repo_fullname)
		assert.Equal(t, expected, actual)
	}
}

func Test_title_case(t *testing.T) {
	cases := map[string]string{
		"":           "",
		"title case": "Title Case",
		"Title case": "Title Case",
		"Title Case": "Title Case",
		"title-case": "Title-Case",
		"title_case": "Title_case",
		"TITLE CASE": "Title Case",
	}
	for given, expected := range cases {
		assert.Equal(t, expected, title_case(given))
	}
}

func Test_interface_number_to_flavor(t *testing.T) {
	cases := map[string]Flavor{
		"10000":  VanillaFlavor,
		"13000":  VanillaFlavor,
		"20000":  TBCFlavor,
		"20500":  TBCFlavor,
		"30000":  WrathFlavor,
		"30400":  WrathFlavor,
		"30403":  WrathFlavor,
		"40000":  CataFlavor,
		"40400":  CataFlavor,
		"50000":  MistsFlavor,
		"100206": MainlineFlavor,
		"110000": MainlineFlavor,
		"120000": MainlineFlavor,
		// "130000": ...

		// whitespace is ignored

		" 100206 ": MainlineFlavor,
	}
	for interface_number, expected := range cases {
		actual, err := interface_number_to_flavor(interface_number)
		assert.Nil(t, err)
		assert.Equal(t, expected, actual)
	}
}

func Test_interface_value_to_flavor_list(t *testing.T) {
	cases := map[string][]Flavor{
		"10000": {VanillaFlavor},

		"20000":  {TBCFlavor},
		"30000":  {WrathFlavor},
		"40000":  {CataFlavor},
		"50000":  {MistsFlavor},
		"110000": {MainlineFlavor},

		"10000, 20000":                      {VanillaFlavor, TBCFlavor},
		"10000, 20000, 30000":               {VanillaFlavor, TBCFlavor, WrathFlavor},
		"10000, 20000, 30000, 40000":        {VanillaFlavor, TBCFlavor, WrathFlavor, CataFlavor},
		"10000, 20000, 30000, 40000, 50000": {VanillaFlavor, TBCFlavor, WrathFlavor, CataFlavor, MistsFlavor},

		// from the wiki
		"100206, 40400, 11502": {MainlineFlavor, CataFlavor, VanillaFlavor},

		// whitespace is ignored
		" 100206 ": {MainlineFlavor},
	}
	for interface_number, expected := range cases {
		actual, err := interface_value_to_flavor_list(interface_number)
		assert.Nil(t, err)
		assert.Equal(t, expected, actual)
	}
}

func Test_interface_value_to_flavor_list__bad_cases(t *testing.T) {
	cases := []string{
		"",         // empty
		"990000",   // outside known range
		"100206, ", // trailing comma
	}
	for _, given := range cases {
		_, err := interface_value_to_flavor_list(given)
		assert.NotNil(t, err)
	}
}

func Test_guess_game_track(t *testing.T) {
	cases := map[string][]string{
		"":              {"", ""},
		"foo":           {"", ""},
		"classic":       {"classic", VanillaFlavor},
		"vanilla":       {"vanilla", VanillaFlavor},
		"fooclassicbar": {"classic", VanillaFlavor},
		"foovanillabar": {"vanilla", VanillaFlavor},
	}
	for given, expected := range cases {
		expected_match, expected_flavor := expected[0], expected[1]
		actual_match, actual_flavor := guess_game_track(given)
		assert.Equal(t, expected_match, actual_match)
		assert.Equal(t, expected_flavor, actual_flavor)
	}
}

func Test_format_bytes(t *testing.T) {
	cases := map[int64]string{
		0:                   "0 B",
		1:                   "1 B",
		1023:                "1023 B",
		1024:                "1.0 KiB",
		1536:                "1.5 KiB",
		1048576:             "1.0 MiB",
		1572864:             "1.5 MiB",
		1073741824:          "1.0 GiB",
		10737418240:         "10.0 GiB",
		1099511627776:       "1.0 TiB",
		1125899906842624:    "1.0 PiB",
		1152921504606846976: "1.0 EiB",
	}
	for given, expected := range cases {
		actual := format_bytes(given)
		assert.Equal(t, expected, actual, "format_bytes(%d)", given)
	}
}

func Test_format_duration(t *testing.T) {
	cases := map[time.Duration]string{
		0:                               "0s",
		1 * time.Second:                 "1s",
		30 * time.Second:                "30s",
		59 * time.Second:                "59s",
		60 * time.Second:                "1.0m",
		90 * time.Second:                "1.5m",
		59*time.Minute + 59*time.Second: "60.0m",
		60 * time.Minute:                "1.0h",
		90 * time.Minute:                "1.5h",
		23 * time.Hour:                  "23.0h",
		24 * time.Hour:                  "1.0d",
		36 * time.Hour:                  "1.5d",
		168 * time.Hour:                 "7.0d",
		-1 * time.Second:                "-1s",
		-60 * time.Second:               "-1.0m",
		-60 * time.Minute:               "-1.0h",
		-24 * time.Hour:                 "-1.0d",
	}
	for given, expected := range cases {
		actual := format_duration(given)
		assert.Equal(t, expected, actual, "format_duration(%v)", given)
	}
}

func Test_calculate_percentile(t *testing.T) {
	// empty case
	result := calculate_percentile([]int64{}, 0.95)
	assert.Equal(t, int64(0), result)

	// single element
	result = calculate_percentile([]int64{100}, 0.95)
	assert.Equal(t, int64(100), result)

	// sorted list
	sorted := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	result = calculate_percentile(sorted, 0.50)
	assert.Equal(t, int64(6), result) // 50% of 10 = index 5 (6th element)

	result = calculate_percentile(sorted, 0.95)
	assert.Equal(t, int64(10), result) // 95% of 10 = index 9 (10th element)

	result = calculate_percentile(sorted, 0.99)
	assert.Equal(t, int64(10), result) // 99% of 10 = index 9 (10th element)

	// larger dataset
	large := make([]int64, 1000)
	for i := range large {
		large[i] = int64(i)
	}
	result = calculate_percentile(large, 0.95)
	assert.Equal(t, int64(950), result)
}

func Test_cache_stats_age_calculation(t *testing.T) {
	// Test that age calculation overflows with many old files
	// Simulate realistic cache scenario: 17673 files with ages around 300 days
	var totalAgeDuration time.Duration
	var totalAgeFloat64 float64
	count := 17673

	for i := range count {
		// Ages varying from 1 day to 600 days (simulate real cache)
		ageDays := 1 + (i % 600)
		age := time.Duration(ageDays) * 24 * time.Hour

		// Track using duration (can overflow)
		totalAgeDuration += age

		// Track using float64 (won't overflow)
		totalAgeFloat64 += age.Seconds()
	}

	// Calculate averages
	avgDuration := totalAgeDuration / time.Duration(count)
	avgFloat64 := totalAgeFloat64 / float64(count)
	avgFloat64Duration := time.Duration(avgFloat64 * float64(time.Second))

	// If duration overflowed, it will be negative or wildly incorrect
	// The float64 method should be accurate

	if avgDuration < 0 {
		t.Logf("Duration overflow detected: avgDuration=%v", avgDuration)
		t.Logf("Correct average (via float64): %v", avgFloat64Duration)

		// The avgDuration should NOT be negative
		// This test documents the overflow bug
		assert.True(t, avgDuration < 0, "Duration overflow causes negative average")

		// The float64 calculation should be positive and reasonable
		assert.True(t, avgFloat64Duration > 0, "Float64-based calculation should be positive")
		assert.True(t, avgFloat64Duration < 365*24*time.Hour, "Average should be less than a year")
	}
}

// Test acquiring and releasing a lock
func Test_lock_file(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test_lock")

	fh, err := os.Create(testFile)
	require.NoError(t, err)
	defer fh.Close()

	err = lock_file(fh)
	assert.NoError(t, err, "should be able to acquire lock")

	err = unlock_file(fh)
	assert.NoError(t, err, "should be able to release lock")
}

// Test that file locking prevents concurrent writes from corrupting data
func Test_file_locking_concurrent_writes(t *testing.T) {

	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "concurrent_test")

	var wg sync.WaitGroup
	numGoroutines := 10
	writeOrder := make([]int, 0, numGoroutines)
	var orderMutex sync.Mutex

	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// Each goroutine tries to open and write to the file
			fh, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			require.NoError(t, err)
			defer fh.Close()

			// Acquire exclusive lock - this should serialize access
			err = lock_file(fh)
			require.NoError(t, err)
			defer unlock_file(fh)

			// Record the order in which locks were acquired
			orderMutex.Lock()
			writeOrder = append(writeOrder, id)
			orderMutex.Unlock()

			// Simulate some work while holding the lock
			time.Sleep(10 * time.Millisecond)

			// Write data
			_, err = fh.Write([]byte{byte(id)})
			require.NoError(t, err)
		}(i)
	}

	wg.Wait()

	// Verify all goroutines completed
	assert.Equal(t, numGoroutines, len(writeOrder), "all goroutines should have acquired the lock")

	// Read the file and verify the data
	data, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, numGoroutines, len(data), "file should contain data from all goroutines")
}

func Test_write_zip_cache_entry_with_locking(t *testing.T) {
	// Setup: initialize STATE with a temp directory
	tempDir := t.TempDir()
	originalState := STATE
	STATE = &State{CWD: tempDir}
	defer func() { STATE = originalState }()

	// Create the cache directory
	err := os.MkdirAll(cache_dir(), 0755)
	require.NoError(t, err)

	// Test data
	cacheKey := "test_zip_cache"
	zipContents := map[string][]byte{
		"file1.txt": []byte("content1"),
		"file2.txt": []byte("content2"),
	}

	// Write to cache
	err = write_zip_cache_entry(cacheKey, zipContents)
	assert.NoError(t, err, "should be able to write zip cache entry")

	// Verify the file was created
	cachePath := cache_path(cacheKey)
	_, err = os.Stat(cachePath)
	assert.NoError(t, err, "cache file should exist")

	// Read back and verify
	readContents, err := read_zip_cache_entry(cacheKey, func(s string) bool { return true })
	assert.NoError(t, err, "should be able to read zip cache entry")
	assert.Equal(t, zipContents, readContents, "read contents should match written contents")
}

func Test_write_zip_cache_entry_concurrent(t *testing.T) {
	// Setup: initialize STATE with a temp directory
	tempDir := t.TempDir()
	originalState := STATE
	STATE = &State{CWD: tempDir}
	defer func() { STATE = originalState }()

	// Create the cache directory
	err := os.MkdirAll(cache_dir(), 0755)
	require.NoError(t, err)

	// Test concurrent writes to different cache keys
	var wg sync.WaitGroup
	numGoroutines := 5

	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			cacheKey := "concurrent_zip_test_" + string(rune('a'+id))
			zipContents := map[string][]byte{
				"file.txt": []byte{byte(id)},
			}

			err := write_zip_cache_entry(cacheKey, zipContents)
			assert.NoError(t, err)
		}(i)
	}

	wg.Wait()

	// Verify all cache files were created correctly
	for i := range numGoroutines {
		cacheKey := "concurrent_zip_test_" + string(rune('a'+i))
		cachePath := cache_path(cacheKey)
		_, err := os.Stat(cachePath)
		assert.NoError(t, err, "cache file %d should exist", i)
	}
}

func Test_calculate_total_downloads(t *testing.T) {
	// Empty releases
	assert.Equal(t, 0, calculate_total_downloads([]GithubRelease{}))

	// Single release, single asset
	releases := []GithubRelease{
		{
			Name: "v1.0.0",
			AssetList: []GithubReleaseAsset{
				{Name: "addon.zip", DownloadCount: 100},
			},
		},
	}
	assert.Equal(t, 100, calculate_total_downloads(releases))

	// Single release, multiple assets
	releases = []GithubRelease{
		{
			Name: "v1.0.0",
			AssetList: []GithubReleaseAsset{
				{Name: "addon.zip", DownloadCount: 100},
				{Name: "addon-nolib.zip", DownloadCount: 50},
				{Name: "release.json", DownloadCount: 5},
			},
		},
	}
	assert.Equal(t, 155, calculate_total_downloads(releases))

	// Multiple releases, multiple assets
	releases = []GithubRelease{
		{
			Name: "v1.0.0",
			AssetList: []GithubReleaseAsset{
				{Name: "addon.zip", DownloadCount: 100},
				{Name: "release.json", DownloadCount: 5},
			},
		},
		{
			Name: "v0.9.0",
			AssetList: []GithubReleaseAsset{
				{Name: "addon.zip", DownloadCount: 75},
			},
		},
		{
			Name: "v0.8.0",
			AssetList: []GithubReleaseAsset{
				{Name: "addon.zip", DownloadCount: 50},
				{Name: "addon-nolib.zip", DownloadCount: 25},
			},
		},
	}
	assert.Equal(t, 255, calculate_total_downloads(releases))

	// Release with no assets
	releases = []GithubRelease{
		{
			Name:      "v1.0.0",
			AssetList: []GithubReleaseAsset{},
		},
		{
			Name: "v0.9.0",
			AssetList: []GithubReleaseAsset{
				{Name: "addon.zip", DownloadCount: 100},
			},
		},
	}
	assert.Equal(t, 100, calculate_total_downloads(releases))
}

func Test_count_releases(t *testing.T) {
	// Empty releases
	assert.Equal(t, 0, count_releases([]GithubRelease{}))

	// Single release
	releases := []GithubRelease{
		{Name: "v1.0.0"},
	}
	assert.Equal(t, 1, count_releases(releases))

	// Multiple releases
	releases = []GithubRelease{
		{Name: "v1.0.0"},
		{Name: "v0.9.0"},
		{Name: "v0.8.0"},
	}
	assert.Equal(t, 3, count_releases(releases))

	// Many releases
	releases = make([]GithubRelease, 100)
	for i := range releases {
		releases[i] = GithubRelease{Name: fmt.Sprintf("v%d.0.0", i)}
	}
	assert.Equal(t, 100, count_releases(releases))
}

func Test_latest_release_is_first_in_slice(t *testing.T) {
	// This test verifies that we correctly identify the latest release
	// GitHub API returns releases in newest-to-oldest order, so the first
	// element should be the newest (not the last)

	newestDate := time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)
	middleDate := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	oldestDate := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)

	releases := []GithubRelease{
		{
			Name:            "v3.0.0",
			PublishedAtDate: newestDate,
			AssetList: []GithubReleaseAsset{
				{Name: "addon.zip", DownloadCount: 10},
			},
		},
		{
			Name:            "v2.0.0",
			PublishedAtDate: middleDate,
			AssetList: []GithubReleaseAsset{
				{Name: "addon.zip", DownloadCount: 50},
			},
		},
		{
			Name:            "v1.0.0",
			PublishedAtDate: oldestDate,
			AssetList: []GithubReleaseAsset{
				{Name: "addon.zip", DownloadCount: 100},
			},
		},
	}

	// The latest release should be the FIRST element (index 0), not the last
	latestRelease := releases[0]
	assert.Equal(t, "v3.0.0", latestRelease.Name)
	assert.Equal(t, newestDate, latestRelease.PublishedAtDate)

	// Verify we're NOT getting the oldest release (which would be at the end)
	oldestRelease := releases[len(releases)-1]
	assert.Equal(t, "v1.0.0", oldestRelease.Name)
	assert.Equal(t, oldestDate, oldestRelease.PublishedAtDate)

	// The dates should be different to prove we're getting the right one
	assert.NotEqual(t, latestRelease.PublishedAtDate, oldestRelease.PublishedAtDate)
	assert.True(t, latestRelease.PublishedAtDate.After(oldestRelease.PublishedAtDate),
		"Latest release should have a newer date than oldest release")
}

func Test_select_release(t *testing.T) {
	given := []GithubRelease{
		{Name: "v3.0.0"},
		{Name: "v2.0.0"},
		{Name: "v1.0.0"},
	}

	cases := map[int]string{
		1: "v3.0.0", // the latest release
		2: "v2.0.0", // the release before it, used by `REPO_EXCEPTIONS`
		3: "v1.0.0",
	}

	for release_number, expected := range cases {
		actual, err := select_release(given, release_number)
		require.NoError(t, err)
		assert.Equal(t, expected, actual.Name)
	}
}

func Test_select_release__bad_cases(t *testing.T) {
	given := []GithubRelease{
		{Name: "v1.0.0"},
	}

	cases := []int{
		0,  // below the first release
		-1, // nonsense
		2,  // beyond the last release
	}

	for _, release_number := range cases {
		_, actual := select_release(given, release_number)
		assert.ErrorIs(t, actual, ErrNoReleasesFound)
	}

	// a repository with no releases at all
	_, actual := select_release([]GithubRelease{}, 1)
	assert.ErrorIs(t, actual, ErrNoReleasesFound)
}

// returns the index of the named CSV column, so tests don't hardcode one.
func csv_column_idx(t *testing.T, name string) int {
	t.Helper()
	idx := slices.Index(ProjectCSVHeader(), name)
	require.NotEqual(t, -1, idx, "no such CSV column: %s", name)
	return idx
}

func Test_project_to_csv_row__nil_last_seen(t *testing.T) {
	// `write_json` sets `LastSeenDate` to nil, so a Project may reach the CSV
	// writer without one.
	given := Project{
		GithubRepo:   GithubRepo{ID: 1, Name: "Foo", FullName: "bar/Foo"},
		LastSeenDate: nil,
	}

	var actual []string
	require.NotPanics(t, func() {
		actual = project_to_csv_row(given)
	})

	expected := ""
	assert.Equal(t, expected, actual[csv_column_idx(t, "last_seen")])
	assert.Len(t, actual, len(ProjectCSVHeader()))
}

func Test_project_to_csv_row__last_seen(t *testing.T) {
	last_seen := time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC)
	given := Project{
		GithubRepo:   GithubRepo{ID: 1, Name: "Foo", FullName: "bar/Foo"},
		LastSeenDate: &last_seen,
	}

	expected := "2025-11-01T00:00:00Z"
	actual := project_to_csv_row(given)
	assert.Equal(t, expected, actual[csv_column_idx(t, "last_seen")])
}

// --- search slice bisection

// a corpus of results that a stubbed `search_fetcher` searches.
// `value_of` maps a result index to the value of its sliced attribute:
// file size in bytes for code searches, creation day for repository searches.
type fake_corpus struct {
	num_results int
	value_of    func(idx int) int
	queries     []string // every query the fetcher was asked for, in order
}

// parses the `size:lo..hi` qualifier out of `search_query`.
// returns the full range when there is no qualifier.
func parse_size_qualifier(t *testing.T, search_query string) (int, int) {
	t.Helper()
	idx := strings.Index(search_query, "size:")
	if idx == -1 {
		return 0, SEARCH_MAX_FILE_SIZE
	}
	var lo, hi int
	_, err := fmt.Sscanf(search_query[idx:], "size:%d..%d", &lo, &hi)
	require.NoError(t, err)
	return lo, hi
}

// parses the `created:from..to` qualifier out of `search_query`, returning
// the bounds as days from `SEARCH_EPOCH`.
func parse_created_qualifier(t *testing.T, search_query string) (int, int) {
	t.Helper()
	idx := strings.Index(search_query, "created:")
	require.NotEqual(t, -1, idx, "no created: qualifier in %q", search_query)
	from_str, to_str, found := strings.Cut(search_query[idx+len("created:"):], "..")
	require.True(t, found, "no '..' in created qualifier of %q", search_query)
	from, err := time.Parse(time.DateOnly, from_str)
	require.NoError(t, err)
	to, err := time.Parse(time.DateOnly, strings.Fields(to_str)[0])
	require.NoError(t, err)
	return int(from.Sub(SEARCH_EPOCH).Hours() / 24), int(to.Sub(SEARCH_EPOCH).Hours() / 24)
}

// builds a `search_fetcher` over `corpus` that honours `size:` and `created:`
// slicing and Github's result window.
func stub_fetcher(t *testing.T, corpus *fake_corpus) search_fetcher {
	t.Helper()
	return func(endpoint, search_query string, page, per_page int) (string, error) {
		corpus.queries = append(corpus.queries, search_query)
		var lo, hi int
		if strings.Contains(search_query, "created:") {
			lo, hi = parse_created_qualifier(t, search_query)
		} else {
			lo, hi = parse_size_qualifier(t, search_query)
		}

		matches := []int{}
		for idx := range corpus.num_results {
			value := corpus.value_of(idx)
			if value >= lo && value <= hi {
				matches = append(matches, idx)
			}
		}

		// github never serves results beyond its window
		servable := min(len(matches), SEARCH_RESULT_WINDOW)
		start := min((page-1)*per_page, servable)
		end := min(page*per_page, servable)
		items := []string{}
		for _, idx := range matches[start:end] {
			items = append(items, fmt.Sprintf(`{"id":%d}`, idx))
		}
		return fmt.Sprintf(`{"total_count":%d,"items":[%s]}`, len(matches), strings.Join(items, ",")), nil
	}
}

// counts the result items across every page blob in `results`.
func count_items(t *testing.T, results []string) int {
	t.Helper()
	total := 0
	for _, blob := range results {
		total += len(gjson.Get(blob, "items").Array())
	}
	return total
}

func Test_size_qualifier(t *testing.T) {
	given := "CF_API_KEY path:.github/workflows"
	expected := "CF_API_KEY path:.github/workflows size:0..999"
	assert.Equal(t, expected, size_qualifier(given, 0, 999))
}

func Test_search_size_slice__fits_in_window(t *testing.T) {
	// a query small enough to need no splitting at all.
	corpus := &fake_corpus{num_results: 250, value_of: func(idx int) int { return idx }}
	actual := search_size_slice(stub_fetcher(t, corpus), "code", "foo", 0, SEARCH_MAX_FILE_SIZE)

	assert.Equal(t, 250, count_items(t, actual))
	// one probe, then three pages of 100.
	assert.Len(t, corpus.queries, 4)
}

func Test_search_size_slice__splits_over_threshold(t *testing.T) {
	// more results than the split threshold but fewer than the window:
	// bisection must still kick in, because `total_count` under-reports.
	corpus := &fake_corpus{num_results: 900, value_of: func(idx int) int { return idx }}
	actual := search_size_slice(stub_fetcher(t, corpus), "code", "foo", 0, SEARCH_MAX_FILE_SIZE)

	assert.Equal(t, 900, count_items(t, actual))
	assert.Greater(t, len(corpus.queries), 9, "expected the slice to be split")
}

func Test_search_size_slice__recovers_results_beyond_window(t *testing.T) {
	// the case that motivated this change: more results than Github will
	// serve for any single query.
	given := 2014
	corpus := &fake_corpus{num_results: given, value_of: func(idx int) int { return idx }}
	actual := search_size_slice(stub_fetcher(t, corpus), "code", "foo", 0, SEARCH_MAX_FILE_SIZE)

	assert.Equal(t, given, count_items(t, actual), "every result should be retrieved")
}

func Test_search_size_slice__splits_when_saturated_despite_low_count(t *testing.T) {
	// `total_count` claims the slice fits, but the pages say otherwise.
	// the pages win.
	corpus := &fake_corpus{num_results: 1500, value_of: func(idx int) int { return idx }}
	fetch := stub_fetcher(t, corpus)
	// the probe under-reports so the slice is never split up front, but the
	// paged responses still fill the window. only saturation reveals the truth.
	lying_fetch := func(endpoint, search_query string, page, per_page int) (string, error) {
		body, err := fetch(endpoint, search_query, page, per_page)
		if err != nil {
			return "", err
		}
		is_probe := per_page == 1
		total := gjson.Get(body, "total_count").Int()
		if is_probe && total > int64(SEARCH_SPLIT_THRESHOLD) {
			body = strings.Replace(body,
				fmt.Sprintf(`"total_count":%d`, total),
				fmt.Sprintf(`"total_count":%d`, SEARCH_SPLIT_THRESHOLD-100), 1)
		}
		return body, nil
	}

	actual := search_size_slice(lying_fetch, "code", "foo", 0, SEARCH_MAX_FILE_SIZE)
	assert.Equal(t, 1500, count_items(t, actual), "saturation should trigger a split regardless of total_count")
}

func Test_search_size_slice__unsplittable_saturated_slice(t *testing.T) {
	// every file is exactly one byte, so the range cannot be narrowed.
	// the excess is unreachable: keep what we have rather than fail.
	corpus := &fake_corpus{num_results: 1500, value_of: func(idx int) int { return 1 }}

	var actual []string
	require.NotPanics(t, func() {
		actual = search_size_slice(stub_fetcher(t, corpus), "code", "foo", 1, 1)
	})
	assert.Equal(t, SEARCH_RESULT_WINDOW, count_items(t, actual), "a full window should still be returned")
}

func Test_search_size_slice__empty_slice(t *testing.T) {
	// a zero `total_count` is not trusted on its own: the slice is confirmed
	// empty by a page that returns no results.
	corpus := &fake_corpus{num_results: 0, value_of: func(idx int) int { return idx }}
	actual := search_size_slice(stub_fetcher(t, corpus), "code", "foo", 0, SEARCH_MAX_FILE_SIZE)

	assert.Empty(t, actual)
	assert.Len(t, corpus.queries, 2, "one probe, then one page to confirm the slice really is empty")
}

func Test_search_size_slice__understated_empty_slice(t *testing.T) {
	// Github reports zero but serves results anyway. the page wins.
	given := 40
	corpus := &fake_corpus{num_results: given, value_of: func(idx int) int { return idx }}
	fetch := stub_fetcher(t, corpus)
	lying_fetch := func(endpoint, search_query string, page, per_page int) (string, error) {
		body, err := fetch(endpoint, search_query, page, per_page)
		if err != nil {
			return "", err
		}
		if per_page == 1 {
			body = strings.Replace(body, fmt.Sprintf(`"total_count":%d`, given), `"total_count":0`, 1)
		}
		return body, nil
	}

	actual := search_size_slice(lying_fetch, "code", "foo", 0, SEARCH_MAX_FILE_SIZE)
	assert.Equal(t, given, count_items(t, actual), "a zero total must not skip fetching the slice")
}

func Test_search_size_slice__slices_are_disjoint(t *testing.T) {
	// no result should be fetched twice: adjacent slices must not overlap.
	corpus := &fake_corpus{num_results: 2014, value_of: func(idx int) int { return idx }}
	results := search_size_slice(stub_fetcher(t, corpus), "code", "foo", 0, SEARCH_MAX_FILE_SIZE)

	seen := map[string]bool{}
	for _, blob := range results {
		for _, item := range gjson.Get(blob, "items").Array() {
			id := item.Get("id").String()
			assert.False(t, seen[id], "result %s fetched more than once", id)
			seen[id] = true
		}
	}
	assert.Len(t, seen, 2014)
}

func Test_more_pages(t *testing.T) {
	// (page, total) => expected remaining pages, 100 per page.
	// a partial final page still needs fetching.
	cases := []struct {
		page, total, expected int
	}{
		{1, 50, 0},   // single partial page
		{1, 100, 0},  // single exact page
		{1, 250, 2},  // pages 2 and 3, the last one partial
		{2, 250, 1},  // page 3, partial
		{3, 250, 0},  // nothing left
		{1, 743, 7},  // pages 2-8, the last one partial
		{7, 743, 1},  // page 8, partial
		{8, 743, 0},  // nothing left
		{1, 1000, 9}, // exact multiple, no partial page
		{10, 1000, 0},
		{10, 2014, 11}, // beyond the window: caller decides what to do
	}
	for _, c := range cases {
		jsonstr := fmt.Sprintf(`{"total_count":%d}`, c.total)
		actual, total, err := more_pages(c.page, 100, jsonstr)
		require.NoError(t, err)
		assert.Equal(t, c.total, total)
		assert.Equal(t, c.expected, actual, "page %d of %d results", c.page, c.total)
	}
}

func Test_more_pages__missing_total_count(t *testing.T) {
	_, _, err := more_pages(1, 100, `{"items":[]}`)
	assert.Error(t, err)
}

func Test_search_url(t *testing.T) {
	// `/search/code` ignores sort and order, so sending them just re-fetches
	// the same page. the size qualifier must survive url encoding.
	given := size_qualifier("CF_API_KEY path:.github/workflows", 0, 999)
	actual := search_url("code", given, 1, 100)

	assert.Contains(t, actual, "size%3A0..999")
	assert.NotContains(t, actual, "sort=")
	assert.NotContains(t, actual, "order=")
	assert.Contains(t, actual, "per_page=100")
	assert.Contains(t, actual, "page=1")
	assert.Contains(t, actual, "/search/code?")
}

func Test_ceil_div(t *testing.T) {
	cases := []struct {
		a, b, expected int
	}{
		{0, 100, 0},
		{1, 100, 1},
		{99, 100, 1},
		{100, 100, 1},
		{101, 100, 2},
		{443, 100, 5},
		{-1, 100, 0}, // already past the end
	}
	for _, c := range cases {
		assert.Equal(t, c.expected, ceil_div(c.a, c.b), "ceil_div(%d, %d)", c.a, c.b)
	}
}

func Test_search_size_slice__paginates_past_understated_total(t *testing.T) {
	// Github under-reports `total_count` on sliced code searches and keeps
	// serving results past it. observed live: a slice reporting 483 results
	// served 693. pagination must follow the pages, not the count.
	given := 693
	corpus := &fake_corpus{num_results: given, value_of: func(idx int) int { return idx }}
	fetch := stub_fetcher(t, corpus)
	understating_fetch := func(endpoint, search_query string, page, per_page int) (string, error) {
		body, err := fetch(endpoint, search_query, page, per_page)
		if err != nil {
			return "", err
		}
		total := gjson.Get(body, "total_count").Int()
		if total == int64(given) {
			body = strings.Replace(body,
				fmt.Sprintf(`"total_count":%d`, given), `"total_count":483`, 1)
		}
		return body, nil
	}

	actual := search_size_slice(understating_fetch, "code", "foo", 0, SEARCH_MAX_FILE_SIZE)
	assert.Equal(t, given, count_items(t, actual), "every served result should be fetched, not just the reported total")
}

// --- repository search date slicing

func Test_created_qualifier(t *testing.T) {
	given := "topic:wow-addon"
	cases := []struct {
		lo, hi   int
		expected string
	}{
		{0, 0, "topic:wow-addon created:2007-01-01..2007-01-01"}, // epoch day, single-day slice
		{0, 30, "topic:wow-addon created:2007-01-01..2007-01-31"},
		{365, 365, "topic:wow-addon created:2008-01-01..2008-01-01"}, // 2007 is not a leap year
	}
	for _, c := range cases {
		assert.Equal(t, c.expected, created_qualifier(given, c.lo, c.hi))
	}
}

func Test_search_max_created_day(t *testing.T) {
	// the upper bound must cover repositories created today: rendering it
	// yields tomorrow's UTC date, so today always falls inside the range.
	expected := time.Now().UTC().AddDate(0, 0, 1).Format(time.DateOnly)
	actual := created_qualifier("foo", 0, search_max_created_day())
	assert.True(t, strings.HasSuffix(actual, ".."+expected), "%q should end with ..%s", actual, expected)
}

func Test_search_created_slice__recovers_results_beyond_window(t *testing.T) {
	// the case that motivated this change: `topic:wow-addon` passed 1000
	// results, more than Github serves for any single query.
	given := 1245
	// repositories created across ten years of days.
	corpus := &fake_corpus{num_results: given, value_of: func(idx int) int { return idx % 3650 }}
	actual := search_created_slice(stub_fetcher(t, corpus), "repositories", "topic:wow-addon", 0, 7300)

	assert.Equal(t, given, count_items(t, actual), "every result should be retrieved")

	// no result fetched twice: adjacent date slices must not overlap.
	seen := map[string]bool{}
	for _, blob := range actual {
		for _, item := range gjson.Get(blob, "items").Array() {
			id := item.Get("id").String()
			assert.False(t, seen[id], "result %s fetched more than once", id)
			seen[id] = true
		}
	}
	assert.Len(t, seen, given)
}

func Test_search_created_slice__single_day_saturates(t *testing.T) {
	// every repository created on the same day: the range cannot be narrowed.
	// the excess is unreachable: keep what we have rather than fail.
	corpus := &fake_corpus{num_results: 1500, value_of: func(idx int) int { return 1 }}

	var actual []string
	require.NotPanics(t, func() {
		actual = search_created_slice(stub_fetcher(t, corpus), "repositories", "topic:wow-addon", 1, 1)
	})
	assert.Equal(t, SEARCH_RESULT_WINDOW, count_items(t, actual), "a full window should still be returned")
}

func Test_search_url__repositories(t *testing.T) {
	// `/search/repositories` honours sort and order, but date slicing makes
	// them unnecessary and they must never be sent.
	given := created_qualifier("topic:wow-addon", 0, 30)
	actual := search_url("repositories", given, 1, 100)

	assert.Contains(t, actual, "/search/repositories?")
	assert.Contains(t, actual, "created%3A2007-01-01..2007-01-31")
	assert.NotContains(t, actual, "sort=")
	assert.NotContains(t, actual, "order=")
}
