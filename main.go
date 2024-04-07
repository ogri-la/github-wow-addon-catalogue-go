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

type CLI struct {
	In            string
	LogLevelLabel string
	LogLevel      slog.Level
}

type State struct {
	CWD         string
	GithubToken string
	Client      *http.Client
	Schema      *jsonschema.Schema
	CLI         CLI
}

func NewState() *State {
	return &State{}
}

type GameTrack = string

const (
	MainlineGameTrack GameTrack = "mainline"
	ClassicGameTrack  GameTrack = "classic"
	BCCGameTrack      GameTrack = "bcc"
	WrathGameTrack    GameTrack = "wrath"
	CataGameTrack     GameTrack = "cata"
)

var GameTrackList = []GameTrack{
	MainlineGameTrack, ClassicGameTrack, BCCGameTrack, WrathGameTrack, CataGameTrack,
}

var GameTrackAliasMap = map[string]GameTrack{
	"vanilla": ClassicGameTrack,
	"tbc":     BCCGameTrack,
	"wotlkc":  WrathGameTrack,
}

var interface_ranges_labels = []GameTrack{
	MainlineGameTrack,
	ClassicGameTrack,
	MainlineGameTrack,
	BCCGameTrack,
	MainlineGameTrack,
	WrathGameTrack,
	CataGameTrack,
	MainlineGameTrack,
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

type GithubRepoOwner struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

// a Github search result
type GithubRepo struct {
	Name            string          `json:"name"`
	FullName        string          `json:"full_name"`
	Owner           GithubRepoOwner `json:"owner"`
	Description     string          `json:"description"`
	Fork            bool            `json:"fork"`
	HTMLURL         string          `json:"html_url"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
	PushedAt        string          `json:"pushed_at"`
	Homepage        string          `json:"homepage"`
	StargazersCount int             `json:"stargazers_count"`
	Language        string          `json:"language"`
	HasDownloads    bool            `json:"has_downloads"`
	Archived        bool            `json:"archived"`
	Disabled        bool            `json:"disabled"`
	//License         string          `json:"license"` // needs more work
	Topics []string `json:"topics"`
}

type ReleaseJsonEntryMetadata struct {
	Flavor    GameTrack `json:"flavor"`
	Interface int       `json:"interface"`
}

type ReleaseJsonEntry struct {
	Filename string                     `json:"filename"`
	NoLib    bool                       `json:"nolib"`
	Metadata []ReleaseJsonEntryMetadata `json:"metadata"`
}

type ReleaseJson struct {
	ReleaseJsonEntryList []ReleaseJsonEntry `json:"releases"`
}

// a Release has many Assets
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	ContentType        string `json:"content_type"`
}

// a repository release
type GithubRelease struct {
	Name            string  `json:"name"` // "2.2.2"
	AssetList       []Asset `json:"assets"`
	PublishedAtDate string  `json:"published_at"`
}

// what we'll render out
type Project struct {
	//GameTrackList  []string
	Name           string // AdiBags
	FullName       string // AdiAddons/AdiBags
	URL            string
	Description    string
	UpdatedDate    string
	Flavors        []GameTrack // unique/set, rename 'GameTrackList'
	ProjectIDMap   map[string]string
	HasReleaseJSON bool
	LastSeenDate   string
}

func ProjectCSVHeader() []string {
	return []string{
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

func ProjectFromCSVRow(row []string) Project {
	project_id_map := map[string]string{
		"curse_id": row[6],
		"wago_id":  row[7],
		"wowi_id":  row[8],
	}
	flavors := strings.Split(row[5], ",")
	has_release_json := row[9] == "True"

	return Project{
		Name:           row[0],
		FullName:       row[1],
		URL:            row[2],
		Description:    row[3],
		UpdatedDate:    row[4],
		Flavors:        flavors,
		ProjectIDMap:   project_id_map,
		HasReleaseJSON: has_release_json,
		LastSeenDate:   row[10],
	}
}

func title_case(s string) string {
	caser := cases.Title(language.English)
	return caser.String(s)
}

func (p Project) CSVRecord() []string {
	return []string{
		p.Name,
		p.FullName,
		p.URL,
		p.Description,
		p.UpdatedDate,
		strings.Join(p.Flavors, ","),
		p.ProjectIDMap["x-curse-project-id"],
		p.ProjectIDMap["x-wago-id"],
		p.ProjectIDMap["x-wowi-id"],
		title_case(fmt.Sprintf("%v", p.HasReleaseJSON)),
		p.LastSeenDate,
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

var REPO_EXCLUDES = map[string]bool{
	"alchem1ster/AddOns-Update-Tool": true, // Not an add-on
	"alchem1ster/AddOnsFixer":        true, // Not an add-on
	"BilboTheGreedy/Azerite":         true, // Not an add-on
	"Centias/BankItems":              true, // Fork
	"DaMitchell/HelloWorld":          true,
	"dratr/BattlePetCount":           true, // Fork
	"HappyRot/AddOns":                true, // Compilation
	"hippuli/":                       true, // Fork galore
	"JsMacros/":                      true, // Minecraft stuff
	"Kirri777/WorldQuestsList":       true, // Fork
	"livepeer/":                      true, // Minecraft stuff
	"lowlee/MikScrollingBattleText":  true, // Fork
	"lowlee/MSBTOptions":             true, // Fork
	"MikeD89/KarazhanChess":          true, // Hijacking BigWigs' TOC IDs, probably by accident
	"smashedr/MethodAltManager":      true, // Fork
	"wagyourtail/JsMacros":           true, // More Minecraft stuff
	"ynazar1/Arh":                    true, // Fork
}

// --- utils

// cannot continue, exit immediately. use `panic` if you need a stracktrace.
func fatal() {
	fmt.Println("cannot continue")
	os.Exit(1)
}

// assert `b` is true, otherwise panic with message `m`.
// ideally these would be compile-time checks, but eh, can't do that.
func ensure(b bool, m string) {
	if !b {
		panic(m)
	}
}

// when `b` is true, log an error and die.
func die(b bool, m string) {
	if b {
		slog.Error(m)
		fatal()
	}
}

// returns `true` if tests are being run.
func is_testing() bool {
	// https://stackoverflow.com/questions/14249217/how-do-i-know-im-running-within-go-test
	return strings.HasSuffix(os.Args[0], ".test")
}

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

func pprint(thing any) {
	s, _ := json.MarshalIndent(thing, "", "\t")
	fmt.Println(string(s))
}

func in_range(v, s, e int) bool {
	return v >= s && v <= e
}

// --- http utils

// inspects HTTP response `resp` and determines if it was throttled.
func throttled(resp ResponseWrapper) bool {
	if resp.StatusCode == 403 {
		slog.Debug("throttled")
		return true
	}
	return false
}

// inspects HTTP response `resp` and determines how long to wait. then waits.
func wait(resp ResponseWrapper) {
	default_pause := float64(10) // seconds
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
	slog.Info(fmt.Sprintf("waiting %vs", pause))
	time.Sleep(time.Duration(pause) * time.Second)
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
	return hex.EncodeToString(md5sum[:])
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

func cache_expired(path string) bool {
	return false
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
	if err == nil {
		if !cache_expired(cache_path) {
			slog.Debug("HTTP GET cache HIT", "url", req.URL, "cache-path", cache_path)
			return cached_resp, nil
		}
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
	slog.Info("HTTP GET", "url", url)
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

	slog.Info("HTTP GET .zip", "url", url)

	empty_response := map[string][]byte{}

	req, err := http.NewRequestWithContext(trace_context(), http.MethodGet, url, nil)
	if err != nil {
		return empty_response, fmt.Errorf("failed to create request: %w", err)
	}

	for header, header_val := range headers {
		req.Header.Set(header, header_val)
	}

	// ---

	zip_cache_key := make_cache_key(req) + "-zip" // fb9f36f59023fbb3681a895823ae9ba0-zip
	cached_zip_file, err := read_zip_cache_entry(zip_cache_key)
	if err == nil {
		slog.Debug("HTTP GET .zip cache HIT", "url", url, "cache-path", cache_path(zip_cache_key))
		return cached_zip_file, nil
	}

	slog.Debug("HTTP GET .zip cache MISS", "url", url, "cache-path", cache_path(zip_cache_key))

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

	write_zip_cache_entry(zip_cache_key, file_bytes)

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
	num_attempts := 10 // todo: see original for handling "X-RateLimit-Reset"

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
			slog.Info("unsuccessful response from github, waiting and trying again", "url", url, "response", resp.StatusCode, "attempt", i)
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

// builds a regular expression to match .toc filenames and extract known game tracks and aliases.
func toc_filename_regexp() *regexp.Regexp {
	game_track_list := []string{}
	for _, game_track := range GameTrackList {
		game_track_list = append(game_track_list, string(game_track))
	}
	for game_track_alias := range GameTrackAliasMap {
		game_track_list = append(game_track_list, game_track_alias)
	}
	game_tracks := strings.Join(game_track_list, "|") // "mainline|wrath|somealias"

	pattern := fmt.Sprintf(`(?i)^(?P<name>[^-]+)(?:[-_](?P<flavor>%s))?\.toc$`, game_tracks)
	return regexp.MustCompile(pattern)
}

var TOC_FILENAME_REGEXP = toc_filename_regexp()

// parses the given `filename`,
// extracting the filename sans ext and any game track,
// returning a pair of (filename, game track).
// matching is case insensitive and game tracks, if any, are returned lowercase.
// when game track is absent, the second value is empty.
// when filename cannot be parsed, both values are empty.
func parse_toc_filename(filename string) (string, GameTrack) {
	matches := TOC_FILENAME_REGEXP.FindStringSubmatch(filename)

	if len(matches) == 2 {
		// "Bar.toc" => [Bar.toc Bar]
		return matches[1], ""
	}
	if len(matches) == 3 {
		// "Bar-wrath.toc" => [Bar-wrath.toc, Bar, wrath]
		flavor := strings.ToLower(matches[2])
		actual_flavor, is_alias := GameTrackAliasMap[flavor]
		if is_alias {
			return matches[1], actual_flavor
		}
		return matches[1], GameTrack(flavor)
	}
	return "", ""
}

// parses the given `zip_file_entry` 'filename',
// that we expect to look like: 'AddonName/AddonName.toc' or 'AddonName/AddonName-gametrack.ext',
// returning `true` when both the dirname and filename sans ext are equal
// and the gametrack, if present, is valid.
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
	filename, _ := parse_toc_filename(rest)
	return prefix == filename && filename != ""
}

// "30403" => "wrath"
func interface_number_to_flavor(interface_val string) (GameTrack, error) {
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
func extract_game_flavors_from_tocs(release_archive_list []Asset) ([]GameTrack, error) {
	flavors := []GameTrack{}
	for _, release_archive := range release_archive_list {

		// future optimisation: original implementation only reads bytes if toc is 'flavorless'.
		// what might also be interesting is preserving *everything* for analysis later,
		// like finding all "X-*" keys ever used.

		toc_file_map, err := github_zip_download(release_archive.BrowserDownloadURL, is_toc_file)
		if err != nil {
			slog.Error("failed to process remote zip file", "error", err)
			continue
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
		return empty_response, fmt.Errorf("no releases found")
	}

	latest_github_release := release_list[0]
	var release_dot_json *ReleaseJson
	for _, asset := range latest_github_release.AssetList {
		if asset.Name == "release.json" {
			asset_resp, err := github_download_with_retries_and_backoff(asset.BrowserDownloadURL)
			if err != nil {
				return empty_response, fmt.Errorf("failed to download release.json: %w", err)
			}

			// todo: custom unmarshall here to enforce gametrack, coerce aliases, validate, etc
			// todo: do we still have access to the bytes or were they consumed?
			release_dot_json, err = parse_release_dot_json(asset_resp.Bytes)
			if err != nil {
				return empty_response, fmt.Errorf("failed to parse release.json: %w", err)
			}

			break
		}
	}

	flavors := []GameTrack{} // set of "wrath", "classic", etc
	project_id_map := map[string]string{}

	if release_dot_json != nil {
		slog.Info("release.json found, looking for matching asset", "repo", repo.FullName, "release", latest_github_release.Name)

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
		slog.Info("no release.json found, looking for candidate assets")
		release_archives := []Asset{}
		for _, asset := range release_list[0].AssetList {
			if asset.ContentType == "application/zip" || asset.ContentType == "application/x-zip-compressed" {
				if strings.HasSuffix(asset.Name, ".zip") {
					release_archives = append(release_archives, asset)
				}
			}
		}

		if len(release_archives) == 0 {
			return empty_response, fmt.Errorf("failed to find a release.json file or a downloadable addon from the assets")
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
		FullName:       repo.FullName,
		Name:           repo.Name,
		URL:            repo.HTMLURL,
		Description:    repo.Description,
		UpdatedDate:    latest_github_release.PublishedAtDate,
		Flavors:        flavors,
		HasReleaseJSON: release_dot_json != nil,
		LastSeenDate:   time.Now().UTC().Format(time.RFC3339),
		ProjectIDMap:   project_id_map,
	}
	return project, nil
}

// parses many `GithubRepo` structs in to a list of `Project` structs.
// `GithubRepo` structs that fail to parse are excluded from the final list.
func parse_repo_list(repo_list []GithubRepo) []Project {
	project_list := []Project{}
	i := 0 // todo: temporary during development
	for _, repo := range repo_list {
		i += 1
		// todo: remove. this is an arbitrary limit while developing.
		if false && i == 100 {
			break
		}

		project, err := parse_repo(repo)
		if err != nil {
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
	page := 1
	search_query = url.QueryEscape(search_query)
	var remaining_pages int

	// sort and order the search results in different ways in an attempt to get at the addons not being returned.
	// note! these are *deprecated*.
	sort_list := []string{"created", "updated"}
	order_by_list := []string{"asc", "desc"}
	for _, order_by := range order_by_list {
		for _, sort_by := range sort_list {
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

// searches Github for addon repositories,
// converts results to `GithubRepo` structs,
// de-duplicates and sorts results,
// returns a set of unique `GithubRepo` structs.
func get_projects() []GithubRepo {
	struct_map := map[string]GithubRepo{}
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
			_, excluded := REPO_EXCLUDES[repo.FullName]
			if excluded {
				slog.Warn("repo is blacklisted", "repo", repo.FullName)
			} else {
				struct_map[repo.FullName] = repo
			}
		}
	}

	// convert map to a list, then sort the list
	struct_list := []GithubRepo{}
	for _, repo := range struct_map {
		struct_list = append(struct_list, repo)
	}
	slices.SortFunc(struct_list, func(a, b GithubRepo) int {
		return strings.Compare(a.FullName, b.FullName)
	})

	return struct_list
}

func write_csv(project_list []Project) {
	w := csv.NewWriter(os.Stdout)
	w.Write(ProjectCSVHeader())
	for _, p := range project_list {
		w.Write(p.CSVRecord())
	}
	w.Flush()
}

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
	state := NewState()

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

func path_exists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, os.ErrNotExist)
}

func read_cli_args(arg_list []string) CLI {
	cli := CLI{}
	flag.StringVar(&cli.In, "in", "", "path to extant addons.csv file. input is merged with results")
	flag.StringVar(&cli.LogLevelLabel, "log-level", "info", "verbosity level. one of: debug, info, warn, error")
	flag.Parse()

	if cli.In != "" {
		die(!path_exists(cli.In), fmt.Sprintf("input path does not exist: %s", cli.In))
		ext := filepath.Ext(cli.In)
		die(ext == "", fmt.Sprintf("input path has no extension: %s", cli.In))
		die(ext != ".csv", fmt.Sprintf("input path has unsupported extension: %s", ext))
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

	if STATE.CLI.In != "" {
		slog.Info("reading projects from input", "path", STATE.CLI.In)
		input_project_list, err = read_csv(STATE.CLI.In)
		die(err != nil, fmt.Sprintf("%v", err))
		slog.Info("found projects", "num", len(input_project_list))
	}

	slog.Info("searching for new projects")
	github_repo_list := get_projects()
	slog.Info("found projects", "num", len(github_repo_list))

	slog.Info("parsing projects")
	project_list := parse_repo_list(github_repo_list)
	slog.Info("projects parsed", "num", len(github_repo_list), "viable", len(project_list))

	if STATE.CLI.In != "" {
		slog.Info("merging input with search results")

		project_map := map[string]Project{}
		for _, project := range input_project_list {
			project_map[project.FullName] = project
		}

		// new results overwrite old
		for _, project := range project_list {
			project_map[project.FullName] = project
		}

		new_project_list := []Project{}
		for _, project := range project_map {
			new_project_list = append(new_project_list, project)
		}
		slices.SortFunc(new_project_list, func(a, b Project) int {
			return strings.Compare(a.FullName, b.FullName)
		})

		project_list = new_project_list

	}

	write_csv(project_list)
}
