package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptrace"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lmittmann/tint"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/snabb/httpreaderat"
	"github.com/tidwall/gjson"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	bufra "github.com/avvmoto/buf-readerat"
)

// --- utils

// cannot continue, exit immediately without a stacktrace.
// just use `panic` if you do need a stracktrace.
func fatal() {
	fmt.Println("cannot continue")
	os.Exit(1)
}

// when `b` is true, log error `msg` and die quietly.
func die(b bool, msg string) {
	if b {
		slog.Error(msg)
		fatal()
	}
}

// assert `b` is true, otherwise panic with message `msg`.
func ensure(b bool, msg string) {
	if !b {
		panic(msg)
	}
}

// returns `true` if tests are being run.
func is_testing() bool {
	// https://stackoverflow.com/questions/14249217/how-do-i-know-im-running-within-go-test
	return strings.HasSuffix(os.Args[0], ".test")
}

// "title case" => "Title Case"
// `strings.ToTitle` behaves strangely and isn't safe with unicode.
func title_case(s string) string {
	caser := cases.Title(language.English)
	return caser.String(s)
}

// returns just the unique items in `list`.
// order is preserved.
func unique[T comparable](list []T) []T {
	idx := make(map[T]bool)
	var result []T
	for _, item := range list {
		_, present := idx[item]
		if !present {
			idx[item] = true
			result = append(result, item)
		}
	}
	return result
}

// pretty-print a json blob
func quick_json(blob string) string {
	// convert into a simple map then
	var foo map[string]any
	json.Unmarshal([]byte(blob), &foo)

	b, err := json.MarshalIndent(foo, "", "\t")
	if err != nil {
		slog.Error("failed to coerce string blob to json", "blob", blob, "error", err)
		fatal()
	}
	return string(b)
}

// pretty-print any `thing`.
func pprint(thing any) {
	s, _ := json.MarshalIndent(thing, "", "\t")
	fmt.Println(string(s))
}

// returns true if `i` is greater than or equal to the lower bound,
// and not higher than the upper bound.
func in_range(i, lower, upper int) bool {
	return i >= lower && i < upper
}

func path_exists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, os.ErrNotExist)
}

// --- structs

type CLI struct {
	InputFile           string
	InputFileExt        string
	OutputFile          string
	OutputFileExt       string
	LogLevelLabel       string
	LogLevel            slog.Level
	UseExpiredCache     bool
	FilterPattern       string
	FilterPatternRegexp *regexp.Regexp
}

// global state, see `STATE`
type State struct {
	CWD         string
	GithubToken string             // Github credentials, pulled from ENV
	Client      *http.Client       // shared HTTP client for persistent connections
	Schema      *jsonschema.Schema // validate release.json files
	CLI         CLI                // captures args passed in from the command line
	RunStart    time.Time          // time app started
}

// type alias for the WoW 'flavor'
type Flavor = string

const (
	MainlineFlavor Flavor = "mainline"
	ClassicFlavor  Flavor = "classic"
	BCCFlavor      Flavor = "bcc"
	WrathFlavor    Flavor = "wrath"
	CataFlavor     Flavor = "cata"
)

// all known flavours.
var FlavorList = []Flavor{
	MainlineFlavor, ClassicFlavor, BCCFlavor, WrathFlavor, CataFlavor,
}

// mapping of alias=>canonical flavour
var FlavorAliasMap = map[string]Flavor{
	"vanilla": ClassicFlavor,
	"tbc":     BCCFlavor,
	"wotlk":   WrathFlavor,
	"wotlkc":  WrathFlavor,
}

var interface_ranges_labels = []Flavor{
	MainlineFlavor,
	ClassicFlavor,
	MainlineFlavor,
	BCCFlavor,
	MainlineFlavor,
	WrathFlavor,
	CataFlavor,
	MainlineFlavor,
}

var interface_ranges = [][]int{
	{1_00_00, 1_13_00},
	{1_13_00, 2_00_00},
	{2_00_00, 2_05_00},
	{2_05_00, 3_00_00},
	{3_00_00, 3_04_00},
	{3_04_00, 4_00_00},
	{4_04_00, 5_00_00},
	{4_00_00, 11_00_00},
}

// a Github search result.
// different types of search return different types of information.
type GithubRepo struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	FullName    string `json:"full_name"`
	Description string `json:"description"`
	HTMLURL     string `json:"html_url"`
}

type ReleaseJsonEntryMetadata struct {
	Flavor    Flavor `json:"flavor"`
	Interface int    `json:"interface"`
}

type ReleaseJsonEntry struct {
	Filename string                     `json:"filename"`
	NoLib    bool                       `json:"nolib"`
	Metadata []ReleaseJsonEntryMetadata `json:"metadata"`
}

type ReleaseJson struct {
	ReleaseJsonEntryList []ReleaseJsonEntry `json:"releases"`
}

// a release has many assets
type GithubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	ContentType        string `json:"content_type"`
}

// a repository has many releases
type GithubRelease struct {
	Name            string               `json:"name"` // "2.2.2"
	AssetList       []GithubReleaseAsset `json:"assets"`
	PublishedAtDate time.Time            `json:"published_at"`
}

// what we'll render out
type Project struct {
	ID             int               `json:"id"`
	Name           string            `json:"name"`      // AdiBags
	FullName       string            `json:"full-name"` // AdiAddons/AdiBags
	URL            string            `json:"url"`       // https://github/AdiAddons/AdiBags
	Description    string            `json:"description"`
	UpdatedDate    time.Time         `json:"updated-date"`
	FlavorList     []Flavor          `json:"flavor-list"`
	ProjectIDMap   map[string]string `json:"project-id-map,omitempty"` // {"x-wowi-id": "foobar", ...}
	HasReleaseJSON bool              `json:"has-release-json"`
	LastSeenDate   time.Time         `json:"last-seen-date"`
}

func ProjectCSVHeader() []string {
	return []string{
		"id",
		"name",
		"full_name",
		"url",
		"description",
		"last_updated",
		"flavors",
		"curse_id",
		"wago_id",
		"wowi_id",
		"has_release_json",
		"last_seen",
	}
}

// read a csv `row` and return a `Project` struct.
func ProjectFromCSVRow(row []string) Project {
	id, err := strconv.Atoi(row[0])
	if err != nil {
		slog.Error("failed to convert 'id' value in CSV to an integer", "row", row, "val", row[0], "error", err)
		fatal()
	}

	updated_date, err := time.Parse(time.RFC3339, row[5])
	if err != nil {
		slog.Error("failed to convert 'date_updated' value in CSV to an RFC3339 timestamp", "value", row[5], "error", err)
		fatal()
	}

	flavor_list := strings.Split(row[6], ",")
	project_id_map := map[string]string{
		"curse_id": row[7],
		"wago_id":  row[8],
		"wowi_id":  row[9],
	}
	has_release_json := row[10] == "True"

	last_seen, err := time.Parse(time.RFC3339, row[11])
	if err != nil {
		slog.Error("failed to convert 'last_seen' value in CSV to an RFC3339 timestamp", "value", row[11], "error", err)
		fatal()
	}
	// whatever offset was given, convert it to UTC
	last_seen = last_seen.UTC()

	return Project{
		ID:             id,
		Name:           row[1],
		FullName:       row[2],
		URL:            row[3],
		Description:    row[4],
		UpdatedDate:    updated_date,
		FlavorList:     flavor_list,
		ProjectIDMap:   project_id_map,
		HasReleaseJSON: has_release_json,
		LastSeenDate:   last_seen,
	}
}

// read a Project struct `p` and return a csv row.
func ProjectToCSVRow(p Project) []string {
	return []string{
		strconv.Itoa(p.ID),
		p.Name,
		p.FullName,
		p.URL,
		p.Description,
		p.UpdatedDate.Format(time.RFC3339),
		strings.Join(p.FlavorList, ","),
		p.ProjectIDMap["x-curse-project-id"],
		p.ProjectIDMap["x-wago-id"],
		p.ProjectIDMap["x-wowi-id"],
		title_case(fmt.Sprintf("%v", p.HasReleaseJSON)),
		p.LastSeenDate.Format(time.RFC3339),
	}
}

type ResponseWrapper struct {
	*http.Response
	Bytes []byte
	Text  string
}

// -- globals

var STATE *State

var API_URL = "https://api.github.com"

// case insensitive repository prefixes
var REPO_EXCLUDES = map[string]bool{
	"foo/": true, // dummy, for testing
	//"ogri-la/elvui":                            true, // Mirror
	//"ogri-la/tukui":                            true, // Mirror

	"alchem1ster/AddOns-Update-Tool":           true, // Not an add-on
	"alchem1ster/AddOnsFixer":                  true, // Not an add-on
	"Aviana/":                                  true,
	"BilboTheGreedy/Azerite":                   true, // Not an add-on
	"blazer404/TargetCharmsRe":                 true, // Fork
	"Centias/BankItems":                        true, // Fork
	"DaMitchell/HelloWorld":                    true, // Dummy add-on
	"dratr/BattlePetCount":                     true, // Fork
	"gorilla-devs/":                            true, // Minecraft stuff
	"HappyRot/AddOns":                          true, // Compilation
	"hippuli/":                                 true, // Fork galore
	"JsMacros/":                                true, // Minecraft stuff
	"juraj-hrivnak/Underdog":                   true, // Minecraft stuff
	"kamoo1/Kamoo-s-TSM-App":                   true, // Not an add-on
	"Kirri777/WorldQuestsList":                 true, // Fork
	"livepeer/":                                true, // Minecraft stuff
	"lowlee/MikScrollingBattleText":            true, // Fork
	"lowlee/MSBTOptions":                       true, // Fork
	"MikeD89/KarazhanChess":                    true, // Hijacking BigWigs' TOC IDs, probably by accident
	"Oppzippy/HuokanGoldLogger":                true, // Archived
	"pinged-eu/wow-addon-helloworld":           true, // Dummy add-on
	"rePublic-Studios/rPLauncher":              true, // Minecraft stuff
	"smashedr/MethodAltManager":                true, // Fork
	"szjunklol/Accountant":                     true, // Fork
	"unix/curseforge-release":                  true, // Template
	"unrealshape/AddOns":                       true, // Add-on compilation
	"vicitafirea/InterfaceColors-Addon":        true, // Custom client add-on
	"vicitafirea/TimeOfDayIndicator-AddOn":     true, // Custom client add-on
	"vicitafirea/TurtleHardcoreMessages-AddOn": true, // Custom client add-on
	"vicitafirea/WarcraftUI-UpperBar-AddOn":    true, // Custom client add-on
	"wagyourtail/JsMacros":                     true, // More Minecraft stuff
	"WowUp/WowUp":                              true, // Not an add-on
	"ynazar1/Arh":                              true, // Fork
}

// --- http utils

func throttled(resp ResponseWrapper) bool {
	return resp.StatusCode == 403
}

// inspects HTTP response `resp` and determines how long to wait. then waits.
func wait(resp ResponseWrapper) {
	default_pause := float64(5) // seconds.
	pause := default_pause

	// inspect cache to see an example of this value
	val := resp.Header.Get("X-Ratelimit-Reset")
	if val == "" {
		slog.Error("rate limited but no 'X-Ratelimit-Reset' header present.")
		pause = default_pause
	} else {
		int_val, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			slog.Error("failed to convert value of 'X-Ratelimit-Remaining' header to an integer", "val", val)
			pause = default_pause
		} else {
			pause = math.Ceil(time.Until(time.Unix(int_val, 0)).Seconds())
			if pause > 120 {
				slog.Warn("received unusual wait time, using default instead", "x-ratelimit-reset-header", val, "wait-time", pause, "default-wait-time", default_pause)
				pause = default_pause
			}
		}
	}
	if pause > 0 {
		slog.Info("throttled", "pause", pause)
		time.Sleep(time.Duration(pause) * time.Second)
	}
}

// logs whether the HTTP request's underlying TCP connection was re-used.
func trace_context() context.Context {
	client_tracer := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			slog.Debug("HTTP connection reuse", "reused", info.Reused, "remote", info.Conn.RemoteAddr())
		},
	}
	return httptrace.WithClientTrace(context.Background(), client_tracer)
}

// --- caching

var CACHE_DURATION = 24       // hours. how long cached files should live for generally.
var CACHE_DURATION_SEARCH = 2 // hours. how long cached *search* files should live for.
var CACHE_DURATION_ZIP = -1   // hours. how long cached zipfile entries should live for.

// returns a path like "/current/working/dir/output/711f20df1f76da140218e51445a6fc47"
func cache_path(cache_key string) string {
	return fmt.Sprintf(STATE.CWD+"/output/%s", cache_key)
}

// creates a key that is unique to the given `http.Request` URL (including query parameters),
// hashed to an MD5 string and prefixed.
// the result can be safely used as a filename.
func make_cache_key(r *http.Request) string {
	// inconsistent case and url params etc will cause cache misses
	key := r.URL.String()
	md5sum := md5.Sum([]byte(key))
	cache_key := hex.EncodeToString(md5sum[:]) // fb9f36f59023fbb3681a895823ae9ba0
	if strings.HasPrefix(r.URL.Path, "/search") {
		return cache_key + "-search" // fb9f36f59023fbb3681a895823ae9ba0-search
	}
	if strings.HasSuffix(r.URL.Path, ".zip") {
		return cache_key + "-zip"
	}
	if strings.HasSuffix(r.URL.Path, "/release.json") {
		return cache_key + "-release.json"
	}
	return cache_key
}

// reads the cached response as if it were the result of `httputil.Dumpresponse`,
// a status code, followed by a series of headers, followed by the response body.
func read_cache_entry(cache_key string) (*http.Response, error) {
	fh, err := os.Open(cache_path(cache_key))
	if err != nil {
		return nil, err
	}
	return http.ReadResponse(bufio.NewReader(fh), nil)
}

// zipfile caches are JSON maps of zipfile-entry-filenames => base64-encoded-bytes.
func read_zip_cache_entry(zip_cache_key string) (map[string][]byte, error) {
	empty_response := map[string][]byte{}

	fh, err := os.Open(cache_path(zip_cache_key))
	if err != nil {
		return empty_response, err
	}

	data, err := io.ReadAll(fh)
	if err != nil {
		return empty_response, err
	}

	cached_zip_file_contents := map[string]string{}
	err = json.Unmarshal(data, &cached_zip_file_contents)
	if err != nil {
		return empty_response, err
	}

	result := map[string][]byte{}
	for zipfile_entry_filename, zipfile_entry_encoded_bytes := range cached_zip_file_contents {
		bytes, err := base64.StdEncoding.DecodeString(zipfile_entry_encoded_bytes)
		if err != nil {
			return empty_response, err
		}
		result[zipfile_entry_filename] = bytes
	}

	return result, nil
}

func write_zip_cache_entry(zip_cache_key string, zip_file_contents map[string][]byte) error {
	cached_zip_file_contents := map[string]string{}

	for zipfile_entry, zipfile_entry_bytes := range zip_file_contents {
		cached_zip_file_contents[zipfile_entry] = base64.StdEncoding.EncodeToString(zipfile_entry_bytes)
	}

	json_data, err := json.Marshal(cached_zip_file_contents)
	if err != nil {
		return err
	}

	return os.WriteFile(cache_path(zip_cache_key), json_data, 0644)
}

// returns true if the given `path` hasn't been modified for a certain duration.
// different paths have different durations.
// assumes `path` exists.
// returns `true` when an error occurs stat'ing `path`.
func cache_expired(path string) bool {
	if STATE.CLI.UseExpiredCache {
		return false
	}

	bits := strings.Split(filepath.Base(path), "-")
	suffix := ""
	if len(bits) == 2 {
		suffix = bits[1]
	}

	var cache_duration_hrs int
	switch suffix {
	case "-search":
		cache_duration_hrs = CACHE_DURATION_SEARCH
	case "-zip":
		cache_duration_hrs = CACHE_DURATION_ZIP
	case "-release.json":
		cache_duration_hrs = CACHE_DURATION_ZIP
	default:
		cache_duration_hrs = CACHE_DURATION
	}

	if cache_duration_hrs == -1 {
		return false // given `path` never expires
	}

	stat, err := os.Stat(path)
	if err != nil {
		slog.Warn("failed to stat cache file, assuming missing/bad cache file", "cache-path", path, "expired", true)
		return true
	}

	diff := STATE.RunStart.Sub(stat.ModTime())
	hours := int(math.Floor(diff.Hours()))
	return hours >= cache_duration_hrs
}

type FileCachingRequest struct{}

func (x FileCachingRequest) RoundTrip(req *http.Request) (*http.Response, error) {

	// don't handle zip files at all,
	// their caching is handled differently.
	// see: `read_zip_cache_entry` and `write_zip_cache_entry`.
	if strings.HasSuffix(req.URL.String(), ".zip") {
		resp, err := http.DefaultTransport.RoundTrip(req)
		return resp, err
	}

	cache_key := make_cache_key(req)    // "711f20df1f76da140218e51445a6fc47"
	cache_path := cache_path(cache_key) // "/current/working/dir/output/711f20df1f76da140218e51445a6fc47"
	cached_resp, err := read_cache_entry(cache_key)
	if err == nil && !cache_expired(cache_path) {
		// a cache entry was found and it's still valid, use that.
		slog.Debug("HTTP GET cache HIT", "url", req.URL, "cache-path", cache_path)
		return cached_resp, nil
	}
	slog.Debug("HTTP GET cache MISS", "url", req.URL, "cache-path", cache_path, "error", err)

	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		// do not cache error responses
		slog.Error("error with transport", "url", req.URL)
		return resp, err
	}

	if resp.StatusCode == 301 || resp.StatusCode == 302 {
		// we've been redirected to another location.
		// follow the redirect and save it's response under the original cache key.
		// .zip files bypass caching so this should only affect `release.json` files.
		new_url, err := resp.Location()
		if err != nil {
			slog.Error("error with redirect request, no location given", "resp", resp)
			return resp, err
		}
		slog.Debug("request redirected", "requested-url", req.URL, "redirected-to", new_url)

		// make another request, update the `resp`, cache as normal.
		// this allows us to cache regular file like `release.json`.

		// but what happens when the redirect is also redirected?
		// the `client` below isn't attached to this `RoundTrip` transport,
		// so it will keep following redirects.
		// the downside is it will probably create a new connection.
		client := http.Client{}
		resp, err = client.Get(new_url.String())
		if err != nil {
			slog.Error("error with transport handling redirect", "requested-url", req.URL, "redirected-to", new_url, "error", err)
			return resp, err
		}
	}

	if resp.StatusCode > 299 {
		// non-2xx response, skip cache
		bdy, _ := io.ReadAll(resp.Body)
		slog.Debug("request unsuccessful, skipping cache", "code", resp.StatusCode, "body", string(bdy))
		return resp, nil
	}

	fh, err := os.Create(cache_path)
	if err != nil {
		slog.Warn("failed to open cache file for writing", "error", err)
		return resp, nil
	}
	defer fh.Close()

	dumped_bytes, err := httputil.DumpResponse(resp, true)
	if err != nil {
		slog.Warn("failed to dump response to bytes", "error", err)
		return resp, nil
	}

	_, err = fh.Write(dumped_bytes)
	if err != nil {
		slog.Warn("failed to write all bytes in response to cache file", "error", err)
		return resp, nil
	}

	cached_resp, err = read_cache_entry(cache_key)
	if err != nil {
		slog.Warn("failed to read cache file", "error", err)
		return resp, nil
	}
	return cached_resp, nil
}

func download(url string, headers map[string]string) (ResponseWrapper, error) {
	slog.Debug("HTTP GET", "url", url)
	empty_response := ResponseWrapper{}

	// ---

	req, err := http.NewRequestWithContext(trace_context(), http.MethodGet, url, nil)
	if err != nil {
		return empty_response, fmt.Errorf("failed to create request: %w", err)
	}
	for header, header_val := range headers {
		req.Header.Set(header, header_val)
	}

	// ---

	client := STATE.Client
	resp, err := client.Do(req)
	if err != nil {
		return empty_response, fmt.Errorf("failed to fetch '%s': %w", url, err)
	}
	defer resp.Body.Close()

	// ---

	content_bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return empty_response, fmt.Errorf("failed to read response body: %w", err)
	}

	return ResponseWrapper{
		Response: resp,
		Bytes:    content_bytes,
		Text:     string(content_bytes),
	}, nil
}

// just like `download` but adds an 'authorization' header to the request.
func github_download(url string) (ResponseWrapper, error) {
	headers := map[string]string{
		"Authorization": "token " + STATE.GithubToken,
	}
	return download(url, headers)
}

// returns a map of zipped-filename=>uncompressed-bytes of files within a zipfile at `url` whose filenames match `zipped_file_filter`.
func download_zip(url string, headers map[string]string, zipped_file_filter func(string) bool) (map[string][]byte, error) {

	slog.Debug("HTTP GET .zip", "url", url)

	empty_response := map[string][]byte{}

	req, err := http.NewRequestWithContext(trace_context(), http.MethodGet, url, nil)
	if err != nil {
		return empty_response, fmt.Errorf("failed to create request: %w", err)
	}

	for header, header_val := range headers {
		req.Header.Set(header, header_val)
	}

	// ---

	cache_key := make_cache_key(req)
	cached_zip_file, err := read_zip_cache_entry(cache_key)
	if err == nil {
		slog.Debug("HTTP GET .zip cache HIT", "url", url, "cache-path", cache_path(cache_key))
		return cached_zip_file, nil
	}

	slog.Debug("HTTP GET .zip cache MISS", "url", url, "cache-path", cache_path(cache_key))

	// ---

	client := STATE.Client

	// a 'readerat' is an implementation of the built-in Go interface `io.ReaderAt`,
	// that provides a means to jump around within the bytes of a remote file using
	// HTTP Range requests.
	http_readerat, err := httpreaderat.New(client, req, nil)
	if err != nil {
		return empty_response, fmt.Errorf("failed to create a HTTPReaderAt: %w", err)
	}

	// a 'buffered readerat' remembers the bytes read of a `io.ReaderAt` implementation,
	// reducing the number of future reads when the bytes have already been read..
	// in our case it's unlikely to be useful but it also doesn't hurt.
	buffer_size := 1024 * 1024 // 1MiB
	buffered_http_readerat := bufra.NewBufReaderAt(http_readerat, buffer_size)
	zip_rdr, err := zip.NewReader(buffered_http_readerat, http_readerat.Size())

	if err != nil {
		return empty_response, fmt.Errorf("failed to create a zip reader: %w", err)
	}

	file_bytes := map[string][]byte{}

	for _, zipped_file_entry := range zip_rdr.File {
		if zipped_file_filter(zipped_file_entry.Name) {
			slog.Debug("found zip file entry name match", "filename", zipped_file_entry.Name)

			fh, err := zipped_file_entry.Open()
			if err != nil {
				// this file is probably busted, stop trying to read it altogether.
				return empty_response, fmt.Errorf("failed to open zip file entry: %w", err)
			}

			bl, err := io.ReadAll(fh)
			if err != nil {
				// again, file is probably busted, abort.
				return empty_response, fmt.Errorf("failed to read zip file entry: %w", err)
			}

			// note: so much other great stuff available to us in zipped_file! can we use it?

			file_bytes[zipped_file_entry.Name] = bl
		}
	}

	write_zip_cache_entry(cache_key, file_bytes)

	return file_bytes, nil
}

func github_zip_download(url string, zipped_file_filter func(string) bool) (map[string][]byte, error) {
	headers := map[string]string{
		"Authorization": "token " + STATE.GithubToken,
	}
	return download_zip(url, headers, zipped_file_filter)
}

func github_download_with_retries_and_backoff(url string) (ResponseWrapper, error) {
	var resp ResponseWrapper
	var err error
	num_attempts := 5

	for i := 1; i <= num_attempts; i++ {
		resp, err = github_download(url)
		if err != nil {
			return ResponseWrapper{}, err
		}

		if resp.StatusCode == 404 {
			return ResponseWrapper{}, errors.New("not found")
		}

		if throttled(resp) {
			wait(resp)
			continue
		}

		if resp.StatusCode != 200 {
			slog.Warn("unsuccessful response from github, waiting and trying again", "url", url, "response", resp.StatusCode, "attempt", i)
			wait(resp)
			continue
		}

		return resp, nil
	}

	slog.Error("failed to download url after a number of attempts", "url", url, "num-attempts", num_attempts)
	return ResponseWrapper{}, errors.New("failed to download url: " + url)
}

// ---

// simplified .toc file parsing.
// keys are lowercased.
// does not handle duplicate keys, last key wins.
// stops reading keyvals after the first blank line.
func parse_toc_file(filename string, toc_bytes []byte) (map[string]string, error) {
	slog.Info("parsing .toc file", "filename", filename)
	line_list := strings.Split(strings.ReplaceAll(string(toc_bytes), "\r\n", "\n"), "\n")
	interesting_lines := map[string]string{}
	for _, line := range line_list {
		if strings.HasPrefix(line, "##") {
			bits := strings.SplitN(line, ":", 2)
			if len(bits) != 2 {
				slog.Warn("ignoring line in .toc file, key has no value", "filename", filename, "line", line)
				continue
			}
			key, val := bits[0], bits[1]
			key = strings.TrimPrefix(key, "##") // "##Interface:", "## Interface:"
			key = strings.TrimSuffix(key, ":")  // "Interface", " Interface"
			key = strings.TrimSpace(key)        // "Interface"
			key = strings.ToLower(key)          // "interface"
			val = strings.TrimSpace(val)        // "100206"
			slog.Debug("toc", "key", key, "val", val, "filename", filename)
			interesting_lines[key] = val
		}
	}
	return interesting_lines, nil
}

// builds a regular expression to match .toc filenames and extract known flavors and aliases.
func toc_filename_regexp() *regexp.Regexp {
	flavor_list := []string{}
	for _, flavor := range FlavorList {
		flavor_list = append(flavor_list, string(flavor))
	}
	for flavor_alias := range FlavorAliasMap {
		flavor_list = append(flavor_list, flavor_alias)
	}
	flavors := strings.Join(flavor_list, "|") // "mainline|wrath|somealias"
	pattern := fmt.Sprintf(`(?i)^(?P<name>[\w-_.]+?)(?:[-_](?P<flavor>%s))?\.toc$`, flavors)
	return regexp.MustCompile(pattern)
}

var TOC_FILENAME_REGEXP = toc_filename_regexp()

// parses the given `filename`,
// extracting the filename sans ext and any flavors,
// returning a pair of (filename, flavor).
// matching is case insensitive and flavor, if any, are returned lowercase.
// when flavor is absent, the second value is empty.
// when filename cannot be parsed, both values are empty.
func parse_toc_filename(filename string) (string, Flavor) {
	matches := TOC_FILENAME_REGEXP.FindStringSubmatch(filename)

	if len(matches) == 2 {
		// "Bar.toc" => [Bar.toc Bar]
		return matches[1], ""
	}
	if len(matches) == 3 {
		// "Bar-wrath.toc" => [Bar-wrath.toc, Bar, wrath]
		flavor := strings.ToLower(matches[2])
		actual_flavor, is_alias := FlavorAliasMap[flavor]
		if is_alias {
			return matches[1], actual_flavor
		}
		return matches[1], Flavor(flavor)
	}
	return "", ""
}

// parses the given `zip_file_entry` 'filename',
// that we expect to look like: 'AddonName/AddonName.toc' or 'AddonName/AddonName-flavor.ext',
// returning `true` when both the dirname and filename sans ext are equal
// and the flavor, if present, is valid.
func is_toc_file(zip_file_entry string) bool {
	// golang doesn't support backreferences, so we can't use ?P= to match previous captures:
	//   fmt.Sprintf(`^(?P<name>[^/]+)[/](?P=name)(?:[-_](?P<flavor>%s%s))?\.toc$`, ids, aliases)
	// instead we'll split on the first path delimiter and match against the rest of the path,
	// ensuring the prefix matches the 'name' capture group.
	bits := strings.SplitN(zip_file_entry, "/", 2)
	if len(bits) != 2 {
		return false
	}
	prefix, rest := bits[0], bits[1] // "Foo/Bar.toc" => "Foo"
	filename, flavor := parse_toc_filename(rest)
	slog.Debug("zip file entry", "name", zip_file_entry, "prefix", prefix, "rest", rest, "toc-match?", filename, "flavor?", flavor)
	if filename != "" {
		if prefix == filename {
			return true
		} else {
			if strings.EqualFold(prefix, filename) {
				slog.Warn("mixed filename casing", "prefix", prefix, "filename", filename)
				return true
			}
		}
	}
	return false
}

// "30403" => "wrath"
func interface_number_to_flavor(interface_val string) (Flavor, error) {
	interface_int, err := strconv.Atoi(interface_val)
	if err != nil {
		return "", fmt.Errorf("failed to convert interface value to integer: %w", err)
	}

	for i, pair := range interface_ranges {
		if in_range(interface_int, pair[0], pair[1]) {
			return interface_ranges_labels[i], nil
		}
	}

	return "", fmt.Errorf("interface value out of range: %d", interface_int)
}

// downloads a file asset from a github release,
// extracts any .toc files from within it,
// parsing their contents,
// and returning a map of any project-ids to their values.
// for example: {"X-WoWI-ID" => "1234", ...}
func extract_project_ids_from_toc_files(asset_url string) (map[string]string, error) {
	empty_response := map[string]string{}

	toc_file_map, err := github_zip_download(asset_url, is_toc_file)
	if err != nil {
		return empty_response, fmt.Errorf("failed to process remote zip file: %w", err)
	}

	if len(toc_file_map) == 0 {
		slog.Warn("no .toc files found in .zip asset", "url", asset_url)
	}

	selected_key_vals := map[string]string{}
	for zipfile_entry, toc_bytes := range toc_file_map {
		keyvals, err := parse_toc_file(zipfile_entry, toc_bytes)
		if err != nil {
			return empty_response, fmt.Errorf("failed to parse .toc contents: %w", err)
		}
		for key, val := range keyvals {
			if key == "x-curse-project-id" || key == "x-wago-id" || key == "x-wowi-id" {
				selected_key_vals[key] = val
			}
		}
	}

	return selected_key_vals, nil
}

// extract the flavors from the filenames
// for 'flavorless' toc files,
// parse the file contents looking for interface versions
func extract_game_flavors_from_tocs(release_archive_list []GithubReleaseAsset) ([]Flavor, error) {
	flavors := []Flavor{}
	for _, release_archive := range release_archive_list {

		// future optimisation: original implementation only reads bytes if toc is 'flavorless'.
		// what might also be interesting is preserving *everything* for analysis later,
		// like finding all "X-*" keys ever used.

		toc_file_map, err := github_zip_download(release_archive.BrowserDownloadURL, is_toc_file)
		if err != nil {
			slog.Error("failed to process remote zip file", "error", err)
			continue
		}

		if len(toc_file_map) == 0 {
			slog.Warn("no .toc files found in .zip asset", "url", release_archive.BrowserDownloadURL)
		}

		for toc_filename, toc_contents := range toc_file_map {
			_, flavor := parse_toc_filename(toc_filename)
			if flavor != "" {
				flavors = append(flavors, flavor)
			} else {
				// 'flavorless', parse the toc contents
				keyvals, err := parse_toc_file(toc_filename, toc_contents)
				if err != nil {
					// couldn't parse this .toc file for some reason, move on to next .toc file
					slog.Error("failed to parse zip file entry .toc contents", "contents", string(toc_contents), "error", err)
					continue
				}
				interface_value, present := keyvals["interface"]
				if !present {
					slog.Warn("no 'interface' value found in toc file", "filename", toc_filename, "release", release_archive.BrowserDownloadURL)
				} else {
					flavor, err := interface_number_to_flavor(interface_value)
					if err != nil {
						slog.Error("failed to parse interface number to a flavor", "error", err)
						continue
					}
					flavors = append(flavors, flavor)
				}
			}
		}
	}

	return flavors, nil
}

func parse_release_dot_json(release_dot_json_bytes []byte) (*ReleaseJson, error) {

	var raw interface{}
	err := json.Unmarshal(release_dot_json_bytes, &raw)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal release.json bytes into a generic struct for validation: %w", err)
	}

	err = STATE.Schema.Validate(raw)
	if err != nil {
		// future: error data is rich, can something nicer be emitted?
		slog.Warn("failed to validate", "raw", raw, "error", err)
		return nil, fmt.Errorf("release.json file failed to validate against schema: %w", err)
	}

	// data is valid, unmarshal to a ReleaseJson

	var release_dot_json ReleaseJson
	err = json.Unmarshal(release_dot_json_bytes, &release_dot_json)
	if err != nil {
		return nil, fmt.Errorf("failed to parse release.json as JSON: %w", err)
	}

	// todo: coerce any values.
	// for example, alias to canonical

	// ...

	return &release_dot_json, nil
}

// ---

var ErrNoReleasesFound = fmt.Errorf("does not use Github releases")
var ErrNoReleaseCandidateFound = fmt.Errorf("failed to find a release.json file or a downloadable addon from the assets")

// look for "release.json" in release assets
// if found, fetch it, validate it as json and then validate as correct release-json data.
// for each asset in release, 'extract project ids from toc files'
// this seems to involve reading the toc files inside zip files looking for "curse_id", "wago_id", "wowi_id" properties
// a lot of toc data is just being ignored here :( and those properties are kind of rare
// if not found, do the same as above, but for *all* zip files (not just those specified in release.json)
// return a Project struct
func parse_repo(repo GithubRepo) (Project, error) {
	slog.Info("parsing repo", "repo", repo.FullName)

	var empty_response Project

	releases_url := API_URL + fmt.Sprintf("/repos/%s/releases?per_page=1", repo.FullName)

	// fetch addon's current release, if any
	resp, err := github_download_with_retries_and_backoff(releases_url)
	if err != nil {
		return empty_response, fmt.Errorf("failed to download repository release listing: %w", err)
	}

	var release_list []GithubRelease
	err = json.Unmarshal([]byte(resp.Text), &release_list)
	if err != nil {
		// Github response could not be parsed.
		return empty_response, fmt.Errorf("failed to parse repository release listing as JSON: %w", err)
	}

	if len(release_list) != 1 {
		// we're fetching exactly one release, the most recent one.
		// if one doesn't exist, skip repo.
		return empty_response, ErrNoReleasesFound
	}

	latest_github_release := release_list[0]
	var release_dot_json *ReleaseJson
	for _, asset := range latest_github_release.AssetList {
		if asset.Name == "release.json" {
			asset_resp, err := github_download_with_retries_and_backoff(asset.BrowserDownloadURL)
			if err != nil {
				return empty_response, fmt.Errorf("failed to download release.json: %w", err)
			}

			// todo: custom unmarshall here to enforce flavor, coerce aliases, validate, etc
			// todo: do we still have access to the bytes or were they consumed?
			release_dot_json, err = parse_release_dot_json(asset_resp.Bytes)
			if err != nil {
				return empty_response, fmt.Errorf("failed to parse release.json: %w", err)
			}

			break
		}
	}

	flavors := []Flavor{} // set of "wrath", "classic", etc
	project_id_map := map[string]string{}

	if release_dot_json != nil {
		slog.Info("release.json found", "repo", repo.FullName, "release", latest_github_release.Name)

		// ensure at least one release in 'releases' is available
		for _, entry := range release_dot_json.ReleaseJsonEntryList {
			for _, metadata := range entry.Metadata {
				flavors = append(flavors, metadata.Flavor)
			}
		}

		// find the matching asset
		first_release_json_entry := release_dot_json.ReleaseJsonEntryList[0]
		for _, asset := range latest_github_release.AssetList {
			if asset.Name == first_release_json_entry.Filename {
				//slog.Debug("match", "repo", repo.FullName, "asset-name", asset.Name, "release.json-name", first_release_json_entry.Filename)
				project_id_map, err = extract_project_ids_from_toc_files(asset.BrowserDownloadURL)
				if err != nil {
					slog.Error("failed to extract project ids", "error", err)
				}
				break
			}
		}

	} else {

		// there is no release.json file,
		// look for .zip assets instead and try our luck.
		slog.Debug("no release.json found in latest release, looking for .zip files instead")
		release_archives := []GithubReleaseAsset{}
		for _, asset := range release_list[0].AssetList {
			if asset.ContentType == "application/zip" || asset.ContentType == "application/x-zip-compressed" {
				if strings.HasSuffix(asset.Name, ".zip") {
					release_archives = append(release_archives, asset)
				}
			}
		}

		if len(release_archives) == 0 {
			return empty_response, ErrNoReleaseCandidateFound
		}

		// extract flavors ...
		flavors, err = extract_game_flavors_from_tocs(release_archives)
		if err != nil {
			return empty_response, fmt.Errorf("failed to parse .toc files in assets")
		}
	}

	flavors = unique(flavors)
	slices.Sort(flavors)

	project := Project{
		ID:             repo.ID,
		FullName:       repo.FullName,
		Name:           repo.Name,
		URL:            repo.HTMLURL,
		Description:    repo.Description,
		UpdatedDate:    latest_github_release.PublishedAtDate,
		FlavorList:     flavors,
		HasReleaseJSON: release_dot_json != nil,
		LastSeenDate:   STATE.RunStart,
		ProjectIDMap:   project_id_map,
	}
	return project, nil
}

// parses many `GithubRepo` structs in to a list of `Project` structs.
// `GithubRepo` structs that fail to parse are excluded from the final list.
func parse_repo_list(repo_list []GithubRepo) []Project {
	project_list := []Project{}
	for _, repo := range repo_list {
		project, err := parse_repo(repo)
		if err != nil {
			if errors.Is(err, ErrNoReleasesFound) || errors.Is(err, ErrNoReleaseCandidateFound) {
				slog.Info("undownloadable addon, skipping", "repo", repo.FullName, "error", err)
				continue
			}
			slog.Warn("error parsing GithubRepo into a Project, skipping", "repo", repo.FullName, "error", err)
			continue
		}
		project_list = append(project_list, project)
	}
	return project_list
}

// TODO: replace this with extracting the 'next' url from the `Link` header:
// Link: <https://api.github.com/search/code?q=path%3A.github%2Fworkflows+bigwigsmods+packager&per_page=100&page=8>; rel="prev", <https://api.github.com/search/code?q=path%3A.github%2Fworkflows+bigwigsmods+packager&per_page=100&page=10>; rel="next", <https://api.github.com/search/code?q=path%3A.github%2Fworkflows+bigwigsmods+packager&per_page=100&page=10>; rel="last", <https://api.github.com/search/code?q=path%3A.github%2Fworkflows+bigwigsmods+packager&per_page=100&page=1>; rel="first"
// inspects `resp` and determines if there are more pages to fetch.
func more_pages(page, per_page int, jsonstr string) (int, error) {
	val := gjson.Get(jsonstr, "total_count")
	if !val.Exists() {
		return 0, errors.New("expected field 'total_count' not found, cannot paginate")
	}
	total := int(val.Int())
	ptr := page * per_page            // 300
	pos := total - ptr                // 743 - 300 = 443
	remaining_pages := pos / per_page // 4.43
	slog.Debug("pagination", "total-results", total, "current-page", page, "results-per-page", per_page, "remaining-pages", remaining_pages)
	return remaining_pages, nil // 4
}

func search_github(endpoint string, search_query string) []string {
	if endpoint != "code" && endpoint != "repositories" {
		slog.Error("unsupported endpoint", "endpoint", endpoint, "supported-endpoints", []string{"code", "repositories"})
		fatal()
	}
	results := []string{} // blobs of json from github api
	per_page := 100
	search_query = url.QueryEscape(search_query)

	// sort and order the search results in different ways in an attempt to get at the addons not being returned.
	// note! these are *deprecated*.
	sort_list := []string{"created", "updated"}
	order_by_list := []string{"asc", "desc"}
	for _, order_by := range order_by_list {
		for _, sort_by := range sort_list {
			page := 1
			remaining_pages := 0
			for {
				url := API_URL + fmt.Sprintf("/search/%s?q=%s&per_page=%d&page=%d&sort=%s&order=%s", endpoint, search_query, per_page, page, sort_by, order_by)
				resp, err := github_download_with_retries_and_backoff(url)
				if err != nil {
					// halt if we can't fetch every page from each of the search queries.
					slog.Error("error requesting url", "url", url, "error", err)
					fatal()
				}
				body := resp.Text
				results = append(results, body)

				_remaining_pages, err := more_pages(page, per_page, body)
				if err != nil {
					// halt if we can't fetch every page from each of the search queries.
					slog.Error("error finding next page of results", "current-page", page, "remaining-pages", remaining_pages, "error", err)
					fatal()
				}
				remaining_pages = _remaining_pages

				if remaining_pages > 0 {
					page = page + 1
					continue
				}
				break
			}
		}
	}
	return results
}

// converts a single search result item from a single page of results to a `GithubRepo` struct.
// `search_result` is either a 'code' result or a 'repository' result,
// the two types have different sets of available fields.
func search_result_to_struct(search_result string) (GithubRepo, error) {

	// 'code' result, many missing fields
	repo_field := gjson.Get(search_result, "repository")
	var repo GithubRepo
	var err error
	if repo_field.Exists() {
		err = json.Unmarshal([]byte(repo_field.String()), &repo)
		if err != nil {
			slog.Error("failed to unmarshal 'code' search result to GithubRepo struct", "search-result", search_result, "error", err)
			return repo, err
		}

		//fmt.Println("code")
		//fmt.Println(quick_json(search_result))

		return repo, nil
	}

	// 'repository' result
	err = json.Unmarshal([]byte(search_result), &repo)
	if err != nil {
		slog.Error("failed to unmarshal 'repository' search result to GithubRepo struct", "search-result", search_result, "error", err)
		return repo, err
	}

	//fmt.Println("repo")
	//fmt.Println(quick_json(search_result))

	return repo, nil
}

// convert the blobs of json from searching Github into `GithubRepo` structs.
func search_results_to_struct_list(search_results_list []string) []GithubRepo {
	results := []GithubRepo{}
	for _, search_results := range search_results_list {
		item_list := gjson.Get(search_results, "items")
		if !item_list.Exists() {
			slog.Error("no 'items' found in search results", "search-results", search_results)
			panic("programming error")
		}

		for _, item := range item_list.Array() {
			github_repo, err := search_result_to_struct(item.String())
			if err != nil {
				slog.Error("skipping item", "error", err)
				panic("programming error") // todo: remove. temporary while we debug
			}

			if github_repo.Name == "" {
				slog.Error("skipping item, bad search result", "repo", item)
				panic("programming error") // todo: remove. temporary while we debug
			}

			results = append(results, github_repo)
		}
	}
	return results
}

func is_excluded(blacklist map[string]bool, filter *regexp.Regexp, repo_fullname string) (string, bool) {

	// first, check repo against any given regexp.
	if filter != nil {
		// if the repo name doesn't match the filter pattern, the repo is excluded.
		if !filter.MatchString(repo_fullname) {
			return filter.String(), true
		}
	}

	// then, check against the blacklist
	repo_fullname_lower := strings.ToLower(repo_fullname)
	for prefix := range blacklist {
		prefix_lower := strings.ToLower(prefix) // wasteful, I know
		if strings.HasPrefix(repo_fullname_lower, prefix_lower) {
			return prefix, true
		}
	}
	return "", false
}

var WARNED = map[string]bool{}

// searches Github for addon repositories,
// converts results to `GithubRepo` structs,
// de-duplicates and sorts results,
// returns a set of unique `GithubRepo` structs.
func get_projects(filter *regexp.Regexp) []GithubRepo {
	repo_idx := map[int]GithubRepo{}
	search_list := [][]string{
		// order is important.
		// duplicate 'code' results are replaced by 'repositories' results, etc.
		// note! as of 2024-04-07 API search results differ from WEB search results,
		// with fewer results and notable absences.
		{"code", "path:.github/workflows bigwigsmods packager"},
		{"code", "path:.github/workflows CF_API_KEY"},
		{"code", "path:.github/workflows WOWI_API_TOKEN"},
		{"repositories", "topic:wow-addon"},
		{"repositories", "topic:world-of-warcraft-addon"},
		{"repositories", "topic:warcraft-addon"},
		{"repositories", "topics:>2 topic:world-of-warcraft topic:addon"},
	}
	for _, pair := range search_list {
		endpoint, query := pair[0], pair[1]
		search_results := search_github(endpoint, query)
		for _, repo := range search_results_to_struct_list(search_results) {
			pattern, excluded := is_excluded(REPO_EXCLUDES, filter, repo.FullName)
			if excluded {
				_, present := WARNED[repo.FullName]
				if !present {
					slog.Warn("repository is blacklisted", "repo", repo.FullName, "pattern", pattern)
					WARNED[repo.FullName] = true
				}
			} else {
				repo_idx[repo.ID] = repo
			}
		}
	}

	// convert map to a list, then sort the list
	struct_list := []GithubRepo{}
	for _, repo := range repo_idx {
		struct_list = append(struct_list, repo)
	}
	slices.SortFunc(struct_list, func(a, b GithubRepo) int {
		return strings.Compare(a.FullName, b.FullName)
	})

	return struct_list
}

// --- json i/o

// write a list of Projects as a JSON array to the given `output_file`,
// or to stdout if `output_file` is empty.
func write_json(project_list []Project, output_file string) {
	bytes, err := json.MarshalIndent(project_list, "", "\t")
	if err != nil {
		slog.Error("failed to marshal project list to JSON", "error", err)
		fatal()
	}

	if output_file == "" {
		fmt.Println(string(bytes))
		return
	}

	err = os.WriteFile(output_file, bytes, 0644)
	if err != nil {
		slog.Error("failed to write JSON to file", "output-file", output_file, "error", err)
		fatal()
	}
}

// read a list of Projects from the given JSON file at `path`.
func read_json(path string) ([]Project, error) {
	var empty_response []Project
	fh, err := os.Open(path)
	if err != nil {
		return empty_response, fmt.Errorf("failed to open JSON file for reading: %w", err)
	}

	bytes, err := io.ReadAll(fh)
	if err != nil {
		return empty_response, fmt.Errorf("failed to read bytes in JSON file: %w", err)
	}

	project_list := []Project{}
	err = json.Unmarshal(bytes, &project_list)
	if err != nil {
		return empty_response, fmt.Errorf("failed to parse JSON in file: %w", err)
	}

	// todo: validate

	return project_list, nil
}

// --- csv i/o

// write a list of Projects as a CSV to the given `output_file`,
// or to stdout if `output_file` is empty.
func write_csv(project_list []Project, output_file string) {
	var output io.Writer
	if output_file == "" {
		output = os.Stdout
	} else {
		fh, err := os.Create(output_file)
		if err != nil {
			slog.Error("failed to open file for writing CSV", "output-file", output_file, "error", err)
			fatal()
		}
		defer fh.Close()
		output = fh
	}

	writer := csv.NewWriter(output)
	writer.Write(ProjectCSVHeader())
	for _, project := range project_list {
		writer.Write(ProjectToCSVRow(project))
	}
	writer.Flush()

	if output_file != "" {
		slog.Info("wrote CSV file", "output-file", output_file)
	}
}

// read a list of Projects from the given CSV file at `path`.
// CSV structure follows original script.
func read_csv(path string) ([]Project, error) {
	empty_response := []Project{}
	fh, err := os.Open(path)
	if err != nil {
		return empty_response, fmt.Errorf("failed to open input file: %w", err)
	}
	defer fh.Close()
	project_list := []Project{}
	rdr := csv.NewReader(fh)

	rdr.Read() // header

	row_list, err := rdr.ReadAll()
	if err != nil {
		return empty_response, fmt.Errorf("failed to read contents of input file: %w", err)
	}
	for _, row := range row_list {
		project_list = append(project_list, ProjectFromCSVRow(row))
	}
	return project_list, nil
}

// bootstrap

func configure_validator() *jsonschema.Schema {
	label := "release.json"

	compiler := jsonschema.NewCompiler()
	compiler.Draft = jsonschema.Draft4 // todo: either drop schema version or raise this one
	path := "resources/release-json-schema.json"

	file_bytes, err := os.ReadFile(path)
	if err != nil {
		slog.Error("failed to read the json schema", "path", path)
		fatal()
	}

	err = compiler.AddResource(label, bytes.NewReader(file_bytes))
	if err != nil {
		slog.Error("failed to add schema to compiler", "error", err)
		fatal()
	}
	schema, err := compiler.Compile(label)
	if err != nil {
		slog.Error("failed to compile schema", "error", err)
		fatal()
	}

	return schema
}

func init_state() *State {
	state := &State{
		RunStart: time.Now().UTC(),
	}

	token, present := os.LookupEnv("ADDONS_CATALOGUE_GITHUB_TOKEN")
	if !present {
		slog.Error("Environment variable 'ADDONS_CATALOGUE_GITHUB_TOKEN' not present")
		fatal()
	}
	state.GithubToken = token

	cwd, err := os.Getwd()
	if err != nil {
		slog.Error("couldn't find the current working dir to derive a writable location", "error", err)
		fatal()
	}
	state.CWD = cwd

	// attach a http client to global state to reuse http connections
	state.Client = &http.Client{}
	state.Client.Transport = &FileCachingRequest{}

	state.Schema = configure_validator()

	return state
}

func read_cli_args(arg_list []string) CLI {
	cli := CLI{}
	flag.StringVar(&cli.InputFile, "in", "", "path to extant addons.csv file. input is merged with results")
	flag.StringVar(&cli.OutputFile, "out", "", "write results to file and not stdout")
	flag.StringVar(&cli.LogLevelLabel, "log-level", "info", "verbosity level. one of: debug, info, warn, error")
	flag.BoolVar(&cli.UseExpiredCache, "use-expired-cache", false, "ignore whether a cached file has expired")
	flag.StringVar(&cli.FilterPattern, "filter", "", "limit catalogue to addons matching regex")
	flag.Parse()

	if cli.InputFile != "" {
		die(!path_exists(cli.InputFile), fmt.Sprintf("input path does not exist: %s", cli.InputFile))
		ext := filepath.Ext(cli.InputFile)
		die(ext == "", fmt.Sprintf("input path has no extension: %s", cli.InputFile))
		die(ext != ".csv" && ext != ".json", fmt.Sprintf("input path has unsupported extension: %s", ext))
		cli.InputFileExt = ext
	}

	if cli.OutputFile != "" {
		ext := filepath.Ext(cli.OutputFile)
		die(ext == "", fmt.Sprintf("output path has no extension: %s", cli.OutputFile))
		die(ext != ".csv" && ext != ".json", fmt.Sprintf("output path has unsupported extension: %s", ext))
		cli.OutputFileExt = ext
	}

	if cli.FilterPattern != "" {
		pattern, err := regexp.Compile(cli.FilterPattern)
		if err != nil {
			slog.Error(fmt.Sprintf("filter could not be compiled to a regular expression: %v", err.Error()))
			fatal()
		}
		cli.FilterPatternRegexp = pattern
	}

	log_level_label_map := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}
	log_level, present := log_level_label_map[cli.LogLevelLabel]
	die(!present, fmt.Sprintf("unknown log level: %s", cli.LogLevelLabel))
	cli.LogLevel = log_level

	return cli
}

func init() {
	ensure(len(interface_ranges_labels) == len(interface_ranges), "interface ranges are not equal interface range labels")
	if is_testing() {
		return
	}
	STATE = init_state()
	STATE.CLI = read_cli_args(os.Args)
	slog.SetDefault(slog.New(tint.NewHandler(os.Stderr, &tint.Options{Level: STATE.CLI.LogLevel})))
}

func main() {
	var err error
	input_project_list := []Project{}

	if STATE.CLI.InputFile != "" {
		slog.Info("reading projects from input", "path", STATE.CLI.InputFile)
		switch STATE.CLI.InputFileExt {
		case ".csv":
			input_project_list, err = read_csv(STATE.CLI.InputFile)
		case ".json":
			input_project_list, err = read_json(STATE.CLI.InputFile)
		}
		die(err != nil, fmt.Sprintf("%v", err))
		slog.Info("found projects", "num", len(input_project_list))
	}

	slog.Info("searching for new projects")
	github_repo_list := get_projects(STATE.CLI.FilterPatternRegexp)
	slog.Info("found projects", "num", len(github_repo_list))

	slog.Info("parsing projects")
	project_list := parse_repo_list(github_repo_list)
	slog.Info("projects parsed", "num", len(github_repo_list), "viable", len(project_list))

	if len(input_project_list) > 0 {
		slog.Info("merging input with search results")

		project_idx := map[int]Project{}
		for _, project := range input_project_list {
			project_idx[project.ID] = project
		}

		// new results overwrite old
		for _, project := range project_list {
			project_idx[project.ID] = project
		}

		new_project_list := []Project{}
		for _, project := range project_idx {
			new_project_list = append(new_project_list, project)
		}
		slices.SortFunc(new_project_list, func(a, b Project) int {
			return strings.Compare(a.FullName, b.FullName)
		})

		project_list = new_project_list
	}

	switch STATE.CLI.OutputFileExt {
	case ".csv":
		write_csv(project_list, STATE.CLI.OutputFile)
	case ".json":
		write_json(project_list, STATE.CLI.OutputFile)
	default:
		write_json(project_list, "")
	}
}
