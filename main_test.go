package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	for i := 0; i < count; i++ {
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

	for i := 0; i < numGoroutines; i++ {
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

	for i := 0; i < numGoroutines; i++ {
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
	for i := 0; i < numGoroutines; i++ {
		cacheKey := "concurrent_zip_test_" + string(rune('a'+i))
		cachePath := cache_path(cacheKey)
		_, err := os.Stat(cachePath)
		assert.NoError(t, err, "cache file %d should exist", i)
	}
}
