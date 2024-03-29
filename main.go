package main

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptrace"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lmittmann/tint"
	"github.com/snabb/httpreaderat"
	"github.com/tidwall/gjson"

	bufra "github.com/avvmoto/buf-readerat"
)

// assert `b` is true, otherwise panic with message `m`.
// ideally these would be compile-time checks, but eh, can't do that.
func ensure(b bool, m string) {
	if !b {
		panic(m)
	}
}

// ---

type GameTrack string

const (
	MAINLINE GameTrack = "mainline"
	CLASSIC  GameTrack = "classic"
	BCC      GameTrack = "bcc"
	WRATH    GameTrack = "wrath"
)

var GameTrackList = []GameTrack{
	MAINLINE, CLASSIC, BCC, WRATH,
}

var GameTrackAliasMap = map[string]GameTrack{
	"vanilla": CLASSIC,
	"tbc":     BCC,
	"wotlkc":  WRATH,
}

var interface_ranges_labels = []GameTrack{
	MAINLINE,
	CLASSIC,
	MAINLINE,
	BCC,
	MAINLINE,
	WRATH,
	MAINLINE,
}

var interface_ranges = [][]int{
	{1_00_00, 1_13_00},
	{1_13_00, 2_00_00},
	{2_00_00, 2_05_00},
	{2_05_00, 3_00_00},
	{3_00_00, 3_04_00},
	{3_04_00, 4_00_00},
	{4_00_00, 11_00_00},
}

type State struct {
	CWD         string
	GithubToken string
	Client      *http.Client
}

func NewState() *State {
	return &State{}
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
	Url             string          `json:"url"`
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
	Name      string  `json:"name"` // "2.2.2"
	AssetList []Asset `json:"assets"`
}

// what we'll render out
type Project struct {
	Description   string
	DownloadCount int
	//GameTrackList  []string
	Label          string
	Name           string
	Source         string
	SourceId       string
	ProjectIDMap   map[string]string
	TagList        []string
	UpdatedDate    string
	URL            string
	Flavors        []GameTrack // unique/set, rename 'GameTrackList'
	LastSeenDate   string
	HasReleaseJSON bool
}

type ResponseWrapper struct {
	*http.Response
	Text string
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

func quick_json(blob string) string {
	// convert into a simple map then
	var foo map[string]any
	json.Unmarshal([]byte(blob), &foo)

	b, err := json.MarshalIndent(foo, "", "\t")
	if err != nil {
		fatal("failed to coerce to json: ", err)
	}
	return string(b)
}

func pprint(thing any) {
	s, _ := json.MarshalIndent(thing, "", "\t")
	fmt.Println(string(s))
}

func fatal(msg string, err error) {
	if err != nil {
		panic(fmt.Sprintf("%s: %v", msg, err))
	}
	panic(msg)
}

func in_range(v, s, e int) bool {
	return v >= s && v <= e
}

// returns a path like "/current/working/dir/output/711f20df1f76da140218e51445a6fc47"
func CachePath(cache_key string) string {
	return fmt.Sprintf(STATE.CWD+"/output/%s", cache_key)
}

// creates a key that is unique to the given `http.Request` URL (including query parameters),
// hashed to an MD5 string and prefixed.
// the result can be safely used as a filename.
func MakeCacheKey(r *http.Request) string {
	// inconsistent case and url params etc will cause cache misses
	key := r.URL.String()
	md5sum := md5.Sum([]byte(key))
	return hex.EncodeToString(md5sum[:])
}

// reads the cached response as if it were the result of `httputil.Dumpresponse`,
// a status code, followed by a series of headers, followed by the response body.
func ReadCacheEntry(cache_key string) (*http.Response, error) {
	fh, err := os.Open(CachePath(cache_key))
	if err != nil {
		return nil, err
	}
	return http.ReadResponse(bufio.NewReader(fh), nil)
}

type FileCachingRequest struct{}

func (x FileCachingRequest) RoundTrip(req *http.Request) (*http.Response, error) {
	cache_key := MakeCacheKey(req)
	// "/current/working/dir/output/711f20df1f76da140218e51445a6fc47"
	cache_path := CachePath(cache_key)
	cached_resp, err := ReadCacheEntry(cache_key)
	if err != nil {
		slog.Debug("cache MISS", "url", req.URL, "cache-path", cache_path, "error", err)

		resp, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			// do not cache error response, pass through
			slog.Error("error with transport, pass through")
			return resp, err
		}

		// perhaps:
		// if redirect, call self with redirect location (resp.Request.URL)
		// if error, pass through
		//if resp.StatusCode != 200 {
		if resp.StatusCode != 200 {
			// non-200 response, pass through
			slog.Debug("non-200 response, pass through", "code", resp.StatusCode)
			return resp, nil
		}

		fh, err := os.Create(cache_path)
		if err != nil {
			slog.Warn("failed to open cache file for writing", "error", err)
			return resp, nil
		}

		dumped_bytes, err := httputil.DumpResponse(resp, true)
		if err != nil {
			slog.Warn("failed to dump response to bytes", "error", err)
			return resp, nil
		}

		_, err = fh.Write(dumped_bytes)
		if err != nil {
			slog.Warn("failed to write all bytes in response to cache file", "error", err)
			fh.Close()
			return resp, nil
		}
		fh.Close()

		cached_resp, err = ReadCacheEntry(cache_key)
		if err != nil {
			slog.Warn("failed to read cache file", "error", err)
			return resp, nil
		}
		return cached_resp, nil

	} else {
		slog.Debug("cache HIT", "url", req.URL, "cache-path", cache_path)
		return cached_resp, nil
	}

	//return http.DefaultTransport.RoundTrip(r)
}

// client trace to log whether the request's underlying tcp connection was re-used
func trace_context() context.Context {
	client_tracer := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			slog.Debug("HTTP connection reuse", "reused", info.Reused, "remote", info.Conn.RemoteAddr())
		},
	}
	return httptrace.WithClientTrace(context.Background(), client_tracer)
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
		Text:     string(content_bytes),
	}, nil
}

// inspects http response and determines if it was throttled.
func throttled(resp ResponseWrapper) bool {
	if resp.StatusCode == 422 || resp.StatusCode == 403 {
		slog.Debug("throttled")
		return true
	}
	return false
}

// inspects http response and determines how long to wait. then waits.
func wait(resp ResponseWrapper) {
	// TODO: something a bit cleverer than this.
	slog.Debug("waiting 5secs")
	time.Sleep(time.Duration(5) * time.Second)
}

// returns a map of zipped-filename=>uncompressed-bytes of files within a zipfile at `url` whose filenames match `zipped_file_filter`.
func download_zip(url string, headers map[string]string, zipped_file_filter func(string) bool) (map[string][]byte, error) {

	empty_response := map[string][]byte{}

	req, err := http.NewRequestWithContext(trace_context(), http.MethodGet, url, nil)
	if err != nil {
		return empty_response, fmt.Errorf("failed to create request: %w", err)
	}

	for header, header_val := range headers {
		req.Header.Set(header, header_val)
	}

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

	return file_bytes, nil
}

// just like `download` but adds an 'authorization' header to the request.
func github_download(url string) (ResponseWrapper, error) {
	headers := map[string]string{
		"Authorization": "token " + STATE.GithubToken,
	}
	return download(url, headers)
}

func github_zip_download(url string, zipped_file_filter func(string) bool) (map[string][]byte, error) {
	headers := map[string]string{
		"Authorization": "token " + STATE.GithubToken,
	}
	return download_zip(url, headers, zipped_file_filter)
}

// ---

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
			slog.Info("unsuccessful response from github, waiting and trying again", "url", url, "response", resp.StatusCode, "attempt", i)
			wait(resp)
			continue
		}

		return resp, nil
	}

	slog.Error("failed to download url after a number of attempts", "url", url, "num-attempts", num_attempts)
	return ResponseWrapper{}, errors.New("failed to download url: " + url)
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
	ptr := page * per_page                              // 300
	pos := total - ptr                                  // 743 - 300 = 443
	remaining_pages := float64(pos) / float64(per_page) // 4.43
	return int(math.Ceil(remaining_pages)), nil         // 5
}

func _get_projects(endpoint string, query string) []string {
	if endpoint != "code" && endpoint != "repositories" {
		fatal("unsupported endpoint: "+endpoint, nil)
	}
	results_acc := []string{}
	per_page := 100
	num_attempts := 5 // number of attempts to download the URL once throttled.
	page := 1
	query = url.PathEscape(query)

	for {
		var resp ResponseWrapper
		var err error
		api_url := API_URL + fmt.Sprintf("/search/%s?q=%s&per_page=%d&page=%d", endpoint, query, per_page, page)
		var body string
		for i := 1; i <= num_attempts; i++ {
			if i > 1 {
				slog.Debug(fmt.Sprintf("attempt %d", i))
			}
			resp, err = github_download(api_url)
			if err != nil {
				fatal("error requesting url: "+api_url, err)
			}

			if throttled(resp) {
				wait(resp)
				continue
			}

			if resp.StatusCode != 200 {
				slog.Debug("non 200, non 422 response, waiting and trying again", "status", resp.StatusCode)
				wait(resp)
				continue
			}

			body = resp.Text
			results_acc = append(results_acc, body)
			break
		}

		remaining_pages, err := more_pages(page, per_page, body)
		if err != nil {
			slog.Error("failed to paginate", "page", page, "remaining-pages", remaining_pages, "error", err)
		}
		if remaining_pages > 0 && page < 10 {
			slog.Debug(fmt.Sprintf("remaining pages: %d", remaining_pages))
			page = page + 1
			continue
		}
		break
	}
	return results_acc
}

func json_string_to_struct(json_blob string) (GithubRepo, error) {

	// this is a 'code' result, many missing fields.
	repo_field := gjson.Get(json_blob, "repository")
	var repo GithubRepo
	var err error
	if repo_field.Exists() {
		err = json.Unmarshal([]byte(repo_field.String()), &repo)
		if err != nil {
			fmt.Println(quick_json(json_blob))
			slog.Error("failed to unmarshal 'code' json to GithubRepo struct", "error", err)
			return repo, err
		}
		return repo, nil
	}

	err = json.Unmarshal([]byte(json_blob), &repo)
	if err != nil {
		fmt.Println(quick_json(json_blob))
		slog.Error("failed to unmarshall 'repository' json to GithubRepo struct", "error", err)
		return repo, err
	}

	return repo, nil
}

func search_results_to_struct_list(search_results_list []string) []GithubRepo {
	results_acc := []GithubRepo{}
	for _, search_results := range search_results_list {
		item_list := gjson.Get(search_results, "items")
		if !item_list.Exists() {
			slog.Error("no 'items' found in json blob", "json-blob", search_results)
			panic("programming error")
		}

		for _, item := range item_list.Array() {
			g, err := json_string_to_struct(item.String())
			if err != nil {
				slog.Error("skipping item", "error", err)
				panic("programming error") // temporary
			}

			if g.Name == "" {
				slog.Error("skipping item, bad search result", "repo", item)
				panic("programming error") // temporary
			}

			results_acc = append(results_acc, g)
		}
	}
	return results_acc
}

// simplified .toc file parsing.
// keys are lowercased.
// does not handle duplicate keys, last key wins.
func parse_toc_file(toc_bytes []byte) (map[string]string, error) {
	line_list := strings.Split(strings.ReplaceAll(string(toc_bytes), "\r\n", "\n"), "\n")
	interesting_lines := map[string]string{}
	for _, line := range line_list {
		if strings.HasPrefix(line, "##") {
			bits := strings.SplitN(line, ":", 2)
			if len(bits) != 2 {
				slog.Warn("ignoring line in .toc file", "line", line)
				continue
			}
			key, val := bits[0], bits[1]
			key = strings.ToLower(strings.TrimSpace(key))
			val = strings.TrimSpace(val)
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
	for game_track_alias, _ := range GameTrackAliasMap {
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
	for _, toc_bytes := range toc_file_map {
		keyvals, err := parse_toc_file(toc_bytes)
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
				keyvals, err := parse_toc_file(toc_contents)
				if err != nil {
					// couldn't parse this .toc file for some reason, move on to next .toc file
					slog.Error("failed to parse zip file entry .toc contents", "contents", string(toc_contents), "error", err)
					continue
				}
				interface_value, present := keyvals["interface"]
				if !present {
					slog.Warn("no interface value found in toc file")
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

// --- tasks

// look for "release.json" in release assets
// if found, fetch it, validate it as json, validate as correct release-json (schema?)
// for each asset in release, 'extract project ids from toc files'
// this seems to involve reading the toc files inside zip files looking for "curse_id", "wago_id", "wowi_id" properties
// a lot of toc data is just being ignored here :( and those properties are kind of rare
// if not found, do the same as above, but for *all* zip files (not just those specified in release.json)
// return a Project struct
func parse_repo(repo GithubRepo) (Project, error) {

	slog.Info("parsing project", "project", repo.FullName)

	empty_response := Project{}

	url := API_URL + fmt.Sprintf("/repos/%s/releases?per_page=1", repo.FullName)
	for {
		// fetch current release, if any
		resp, err := github_download_with_retries_and_backoff(url)
		if err != nil {
			//slog.Error("error downloading repository release listing", "error", err.Error())
			return empty_response, fmt.Errorf("failed to download repository release listing: %w", err)
		}

		var release_list []GithubRelease
		err = json.Unmarshal([]byte(resp.Text), &release_list)
		if err != nil {
			//slog.Error("error parsing Github 'release' response as JSON", "error", err)
			return empty_response, fmt.Errorf("failed to parse repository release listing as JSON: %w", err)
		}

		if len(release_list) != 1 {
			return empty_response, fmt.Errorf("project has no releases")
		}

		first_github_release := release_list[0] // 'release_json_release'

		pprint(first_github_release)

		var release_json_file *ReleaseJson
		for _, asset := range first_github_release.AssetList {
			if asset.Name == "release.json" {
				asset_resp, err := github_download_with_retries_and_backoff(asset.BrowserDownloadURL)
				if err != nil {
					return empty_response, fmt.Errorf("failed to download release.json: %w", err)
				}

				// todo: custom unmarshall here to enforce gametrack, coerce aliases, validate, etc
				err = json.Unmarshal([]byte(asset_resp.Text), &release_json_file)
				if err != nil {
					return empty_response, fmt.Errorf("failed to parse release.json as JSON: %w", err)
				}
				break
			}
		}

		flavors := []GameTrack{} // set of "wrath", "classic", etc
		project_id_map := map[string]string{}

		if release_json_file != nil {

			slog.Info("release.json found, looking for matching asset")

			// ensure at least one release in 'releases' is available

			for _, entry := range release_json_file.ReleaseJsonEntryList {
				for _, metadata := range entry.Metadata {
					flavors = append(flavors, metadata.Flavor)
				}
			}

			// find the matching asset
			first_release_json_entry := release_json_file.ReleaseJsonEntryList[0]
			for _, asset := range first_github_release.AssetList {
				slog.Debug("match?", "asset-name", asset.Name, "release-name", first_release_json_entry.Filename)
				if asset.Name == first_release_json_entry.Filename {
					project_id_map, err = extract_project_ids_from_toc_files(asset.BrowserDownloadURL)
					if err != nil {
						slog.Error("failed to extract project ids, ignoring", "error", err)
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

		project := Project{
			Name:           repo.Name,
			URL:            repo.Url,
			Description:    repo.Description,
			UpdatedDate:    repo.UpdatedAt,
			Flavors:        flavors,
			HasReleaseJSON: release_json_file != nil,
			LastSeenDate:   time.Now().UTC().Format(time.RFC3339),
			ProjectIDMap:   project_id_map,
		}

		pprint(project)

		panic("stopping")

		return project, nil
	}
}

// parses many `GithubRepo` structs in to a list of `Project` structs.
// `GithubRepo` structs that fail to parse are excluded from the final list.
func parse_repo_list(repo_list []GithubRepo) []Project {
	project_list := []Project{}
	i := 0
	for _, repo := range repo_list {
		i += 1
		if i == 150 {
			break
		}

		project, err := parse_repo(repo)
		if err != nil {
			slog.Warn("skipping project", "project", repo.FullName, "error", err)
			continue
		}
		project_list = append(project_list, project)

	}
	return project_list
}

// does a bunch of Github searches for repositories,
// converts results to structs,
// de-duplicates results,
// returns structs.
func get_projects() []GithubRepo {
	struct_map := map[string]GithubRepo{}
	search_list := [][]string{
		// order is important.
		// duplicate 'code' results are replaced by 'repositories' results, etc.
		{"code", "path:.github/workflows bigwigsmods packager"},
		{"code", "path:.github/workflows CF_API_KEY"},
		{"repositories", "topic:wow-addon"},
		{"repositories", "topics:>2 topic:world-of-warcraft topic:addon"},
	}
	for _, pair := range search_list {
		endpoint := pair[0]
		query := pair[1]
		search_results := _get_projects(endpoint, query)
		for _, repo := range search_results_to_struct_list(search_results) {
			excluded, present := REPO_EXCLUDES[repo.FullName]
			if !present || !excluded {
				struct_map[repo.FullName] = repo
			}
		}
	}
	struct_list := []GithubRepo{}
	for _, repo := range struct_map {
		struct_list = append(struct_list, repo)
	}

	slices.SortFunc(struct_list, func(a, b GithubRepo) int {
		return strings.Compare(a.FullName, b.FullName)
	})

	return struct_list
}

func init_state() *State {
	state := NewState()

	token, present := os.LookupEnv("ADDONS_CATALOGUE_GITHUB_TOKEN")
	if !present {
		panic("Environment variable 'ADDONS_CATALOGUE_GITHUB_TOKEN' not present.")
	}
	state.GithubToken = token

	cwd, err := os.Getwd()
	if err != nil {
		fatal("foo", err)
	}
	state.CWD = cwd

	state.Client = &http.Client{}
	state.Client.Transport = &FileCachingRequest{}

	return state
}

// --- bootstrap

func is_testing() bool {
	// https://stackoverflow.com/questions/14249217/how-do-i-know-im-running-within-go-test
	//return flag.Lookup("test.v") != nil
	return strings.HasSuffix(os.Args[0], ".test")
}

func init() {
	ensure(len(interface_ranges_labels) == len(interface_ranges), "not equal")

	if is_testing() {
		return
	}
	STATE = init_state()
	slog.SetDefault(slog.New(tint.NewHandler(os.Stderr, &tint.Options{Level: slog.LevelDebug})))
}

func main() {
	slog.Info("searching for projects")
	github_repo_list := get_projects()
	slog.Info("found projects", "num", len(github_repo_list))

	slog.Info("parsing projects")
	project_list := parse_repo_list(github_repo_list)
	slog.Info("projects parsed", "viable", len(project_list))
}
