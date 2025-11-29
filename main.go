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
	"sync"

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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lmittmann/tint"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/snabb/httpreaderat"
	flag "github.com/spf13/pflag"
	"github.com/tidwall/gjson"

	bufra "github.com/avvmoto/buf-readerat"
)

var APP_VERSION = "unreleased" // modified at release with `-ldflags "-X main.APP_VERSION=X.X.X"`
var APP_LOC = "https://github.com/ogri-la/github-wow-addon-catalogue-go"

var CACHE_DURATION = 24              // hours. how long cached files should live for generally.
var CACHE_DURATION_SEARCH = 2        // hours. how long cached *search* files should live for.
var CACHE_DURATION_ZIP = -1          // hours. how long cached zipfile entries should live for.
var CACHE_DURATION_RELEASE_JSON = -1 // hours. how long cached release.json entries should live for.

// prevents issuing the same warnings multiple times when going backwards and forwards
// and upside down through the search results.
var WARNED = map[string]bool{}

var API_URL = "https://api.github.com"

// we know the latest release is broken, try the previous one instead.
var REPO_EXCEPTIONS = map[string]bool{
	"TimothyLuke/GSE-Advanced-Macro-Compiler": true, // 2024-06-07, latest release missing assets
}

// projects that do their release over several Github releases.
// this leads to their data flipflopping about.
var REPO_MULTI_RELEASE = map[string]bool{
	"Mortalknight/GW2_UI":                  true,
	"Nevcairiel/GatherMate2":               true,
	"Nevcairiel/Inventorian":               true,
	"Witnesscm/NDui_Plus":                  true,
	"siweia/NDui":                          true,
	"Wutname1/SpartanUI":                   true,
	"xod-wow/LiteBag":                      true, // beta releases missing classic
	"michaelnpsp/Grid2":                    true, // stable releases get the full set, the multiple beta releases get partial assets
	"nebularg/PitBull4":                    true,
	"casualshammy/NameplateCooldowns":      true,
	"Slothpala/RaidFrameSettings":          true, // betas missing release
	"MarcLF/AuctionBuddy":                  true,
	"Syiana/SUI":                           true, // looks like releases have been disabled, but still available at /releases, weird.
	"MotherGinger/RecklessAbandon-Classic": true,
	"sfmict/CursorMod":                     true,
	"sfmict/HidingBar":                     true,
	"valkyrnstudios/RankSentinel":          true, // sometimes cata, sometimes not
	"TorelTwiddler/CanIMogIt":              true,
	"Gogo1951/Open-Sesame":                 true,
	"Gogo1951/TFTB":                        true,

	// these guys are breaking up their releases across multiple github releases,
	// but they're actually doing something good! they're keeping their last major version updated.
	// I'd like to accommodate this somehow. group releases by major version, nolib, game track?
	"1onar/KeyUI": false,
}

var KNOWN_DUPLICATE_LIST = [][]string{
	// Dominos bundles Masque_Dominos
	{"tullamods/Dominos", "SFX-WoW/Masque_Dominos"},
	// RealUI bundles Masque
	{"RealUI/RealUI", "SFX-WoW/Masque"},
	// XiconQoo/RETabBinder is a backport of AcidWeb/RETabBinder
	// - https://github.com/XiconQoo/RETabBinder/issues/1
	{"XiconQoo/RETabBinder", "AcidWeb/RETabBinder"},
}

// case insensitive repository prefixes
var REPO_BLACKLIST = map[string]bool{
	"foo/":                      true, // dummy, for unit tests
	"layday/wow-addon-template": true, // template

	// mine
	"WOWRainbowUI/RainbowUI-Retail": true, // addon bundle, very large, incorrect filestructure
	"WOWRainbowUI/RainbowUI-Era":    true, // addon bundle, very large, incorrect filestructure
	"Expensify/App":                 true, // not an addon, not sure how it made it into list but I noticed it when it disappeared from list

	// layday's blacklist
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
	"leatrix/*":                                true, // 2025-01-27: releases removed
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

type SubCommand = string

const (
	ScrapeSubCommand             SubCommand = "scrape"
	FindDuplicatesSubCommand     SubCommand = "find-duplicates"
	DumpReleaseDotJsonSubCommand SubCommand = "dump-release-dot-json"
	CacheStatsSubCommand         SubCommand = "cache-stats"
	CachePruneSubCommand         SubCommand = "cache-prune"
)

var KNOWN_SUBCOMMANDS = []SubCommand{
	ScrapeSubCommand,
	FindDuplicatesSubCommand,
	DumpReleaseDotJsonSubCommand,
	CacheStatsSubCommand,
	CachePruneSubCommand,
}

type FindDuplicatesCommand struct {
	InputFileList []string
}

type ScrapeCommand struct {
	InputFileList       []string
	OutputFileList      []string
	SkipSearch          bool           // don't search github, just use input files
	UseExpiredCache     bool           // use cached data, even if it's expired
	FilterPattern       string         // a regex to be applied to `project.FullName`
	FilterPatternRegexp *regexp.Regexp // the compiled form of `FilterPattern`
}

type CachePruneCommand struct {
	Delete     bool   // actually delete files (default is dry-run)
	DeleteFlag string // raw flag value for validation
}

type Flags struct {
	SubCommand            SubCommand // cli subcommand, e.g. 'scrape'
	LogLevel              slog.Level
	ScrapeCommand         ScrapeCommand         // cli args for the 'scrape' command
	FindDuplicatesCommand FindDuplicatesCommand // cli args for the 'find-duplicates' command
	CachePruneCommand     CachePruneCommand     // cli args for the 'cache-prune' command
}

// global state, see `STATE`
type State struct {
	CWD         string             // Current Working Directory
	GithubToken string             // Github credentials, pulled from ENV
	Client      *http.Client       // shared HTTP client for persistent connections
	Schema      *jsonschema.Schema // validates release.json files
	Flags       Flags
	RunStart    time.Time // time app started
}

var STATE *State

// limit concurrent HTTP requests
var HTTPSem = make(chan int, 50)

func take_http_token() {
	HTTPSem <- 1
}

func release_http_token() {
	<-HTTPSem
}

// type alias for the WoW 'flavor'.
type Flavor = string

// "For avoidance of doubt, these are all the file name formats presently supported on all clients and the order that each client will attempt to load them in currently.
// On the wiki we're recommending that people use a single specific suffix for each client for overall consistency, which corresponds to the first file in each sub-list below and is the format used by Blizzard."
// - https://github.com/Stanzilla/WoWUIBugs/issues/68#issuecomment-889431675
// - https://wowpedia.fandom.com/wiki/TOC_format
const (
	MainlineFlavor Flavor = "mainline"
	VanillaFlavor  Flavor = "vanilla"
	TBCFlavor      Flavor = "tbc"
	WrathFlavor    Flavor = "wrath"
	CataFlavor     Flavor = "cata"
	MistsFlavor    Flavor = "mists"
)

// all known flavours.
var FLAVOR_LIST = []Flavor{
	MainlineFlavor, VanillaFlavor, TBCFlavor, WrathFlavor, CataFlavor, MistsFlavor,
}

// mapping of alias => canonical flavour
var FLAVOR_ALIAS_MAP = map[string]Flavor{
	"classic": VanillaFlavor,
	"bcc":     TBCFlavor,
	"wotlk":   WrathFlavor,
	"wotlkc":  WrathFlavor,
}

// for sorting output
var FLAVOR_WEIGHTS = map[Flavor]int{
	MainlineFlavor: 0,
	VanillaFlavor:  1,
	TBCFlavor:      2,
	WrathFlavor:    3,
	CataFlavor:     4,
	MistsFlavor:    5,
}

var INTERFACE_RANGES = map[int]Flavor{
	1_00_00:  VanillaFlavor,
	2_00_00:  TBCFlavor,
	3_00_00:  WrathFlavor,
	4_00_00:  CataFlavor,
	5_00_00:  MistsFlavor,
	6_00_00:  MainlineFlavor,
	7_00_00:  MainlineFlavor,
	8_00_00:  MainlineFlavor,
	9_00_00:  MainlineFlavor,
	10_00_00: MainlineFlavor,
	11_00_00: MainlineFlavor,
	12_00_00: MainlineFlavor,
}

// returns a single list of unique, sorted, `Flavor` strings.
func unique_sorted_flavor_list(fll ...[]Flavor) []Flavor {
	flavor_list := flatten(fll...)
	for i, flavor := range flavor_list {
		flavor := strings.ToLower(flavor)
		actual_flavor, is_alias := FLAVOR_ALIAS_MAP[flavor]
		if is_alias {
			flavor_list[i] = actual_flavor
		}
	}
	flavor_list = unique(flavor_list)
	sort.Slice(flavor_list, func(i, j int) bool {
		return FLAVOR_WEIGHTS[flavor_list[i]] < FLAVOR_WEIGHTS[flavor_list[j]]
	})
	return flavor_list
}

// a Github search result.
// different types of search return different types of information,
// this captures a common subset.
type GithubRepo struct {
	ID           int               `json:"id"`
	Name         string            `json:"name"`      // "AdiBags"
	FullName     string            `json:"full_name"` // "AdiAddons/AdiBags"
	URL          string            `json:"html_url"`  // "https://github/AdiAddons/AdiBags"
	Description  string            `json:"description"`
	ProjectIDMap map[string]string `json:"project-id-map,omitempty"` // {"x-wowi-id": "foobar", ...}
}

// read a csv `row` and return a `Project` struct.
func repo_from_csv_row(row []string) GithubRepo {
	id, err := strconv.Atoi(row[0])
	if err != nil {
		slog.Error("failed to convert 'id' value in CSV to an integer", "row", row, "val", row[0], "error", err)
		fatal()
	}

	project_id_map := map[string]string{}
	if row[7] != "" {
		project_id_map["x-curse-project-id"] = row[7]
	}
	if row[8] != "" {
		project_id_map["x-wago-id"] = row[8]
	}
	if row[9] != "" {
		project_id_map["x-wowi-id"] = row[9]
	}

	return GithubRepo{
		ID:          id,
		Name:        row[1],
		FullName:    row[2],
		URL:         row[3],
		Description: row[4],
		// 5 last-updated
		// 6 flavors
		ProjectIDMap: project_id_map,
	}
}

type ReleaseJsonEntryMetadata struct {
	Flavor    Flavor `json:"flavor"`
	Interface int    `json:"interface"`
}

type ReleaseJsonEntry struct {
	Name     string                     `json:"name"`
	Filename string                     `json:"filename"`
	NoLib    bool                       `json:"nolib"`
	Metadata []ReleaseJsonEntryMetadata `json:"metadata"`
}

// a `release.json` file.
type ReleaseDotJson struct {
	ReleaseJsonEntryList []ReleaseJsonEntry `json:"releases"`
}

// a Github release has many assets.
type GithubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	ContentType        string `json:"content_type"`
}

// a Github repository has many releases.
type GithubRelease struct {
	Name            string               `json:"name"` // "2.2.2"
	AssetList       []GithubReleaseAsset `json:"assets"`
	PublishedAtDate time.Time            `json:"published_at"`
}

// result of scraping a repository
type Project struct {
	GithubRepo
	UpdatedDate    time.Time  `json:"updated-date"`
	FlavorList     []Flavor   `json:"flavor-list"`
	HasReleaseJSON bool       `json:"has-release-json"`
	LastSeenDate   *time.Time `json:"last-seen-date,omitempty"`
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

// read a Project struct `p` and return a csv row.
func project_to_csv_row(p Project) []string {
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

// convenience wrapper around a `http.Response`.
type ResponseWrapper struct {
	*http.Response
	Bytes []byte
	Text  string
}

// --- http utils

// returns `true` if given `resp` was throttled.
func throttled(resp ResponseWrapper) bool {
	return resp.StatusCode == 403
}

// inspects `resp` and determines how long to wait. then waits.
func wait(resp ResponseWrapper) {
	default_pause := float64(60) // seconds.
	pause := default_pause

	// inspect cache to see an example of this value
	val := resp.Header.Get("X-RateLimit-Reset")
	if val == "" {
		slog.Debug("rate limited but no 'X-RateLimit-Reset' header present.", "headers", resp.Header)
	} else {
		int_val, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			slog.Error("failed to convert value of 'X-RateLimit-Reset' header to an integer", "val", val)
		} else {
			pause = math.Ceil(time.Until(time.Unix(int_val, 0)).Seconds())
			if pause > 120 {
				slog.Warn("received unusual wait time, using default instead", "X-RateLimit-Reset", val, "wait-time", pause, "default-wait-time", default_pause)
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

// returns a path to the cache directory.
func cache_dir() string {
	return filepath.Join(STATE.CWD, "output") // "/current/working/dir/output"
}

// returns a path to the given `cache_key`.
func cache_path(cache_key string) string {
	return filepath.Join(cache_dir(), cache_key) // "/current/working/dir/output/711f20df1f76da140218e51445a6fc47"
}

// returns a list of cache keys found in the cache directory.
// each key in list can be read with `read_cache_key`.
func cache_entry_list() []string {
	empty_response := []string{}
	dir_entry_list, err := os.ReadDir(cache_dir())
	if err != nil {
		slog.Error("failed to list cache directory", "error", err)
		return empty_response
	}
	file_list := []string{}
	for _, dir_entry := range dir_entry_list {
		if !dir_entry.IsDir() {
			file_list = append(file_list, dir_entry.Name())
		}
	}
	return file_list
}

// creates a key that is unique to the given `req` URL (including query parameters),
// hashed to an MD5 string and prefixed, suffixed.
// the result can be safely used as a filename.
func make_cache_key(req *http.Request) string {
	// inconsistent case and url params etc will cause cache misses
	key := req.URL.String()
	md5sum := md5.Sum([]byte(key))
	cache_key := hex.EncodeToString(md5sum[:]) // fb9f36f59023fbb3681a895823ae9ba0
	if strings.HasPrefix(req.URL.Path, "/search") {
		return cache_key + "-search" // fb9f36f59023fbb3681a895823ae9ba0-search
	}
	if strings.HasSuffix(req.URL.Path, ".zip") {
		return cache_key + "-zip"
	}
	if strings.HasSuffix(req.URL.Path, "/release.json") {
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
// todo: remove zipped_file_filter
func read_zip_cache_entry(zip_cache_key string, zipped_file_filter func(string) bool) (map[string][]byte, error) {
	empty_response := map[string][]byte{}

	data, err := os.ReadFile(cache_path(zip_cache_key))
	if err != nil {
		return empty_response, err
	}

	cached_zip_file_contents := map[string]string{}
	err = json.Unmarshal(data, &cached_zip_file_contents)
	if err != nil {
		return empty_response, err
	}

	result := map[string][]byte{}
	//placeholder := []byte{0x66, 0xFC, 0x72}
	for zipfile_entry_filename, zipfile_entry_encoded_bytes := range cached_zip_file_contents {
		decoded_bytes, err := base64.StdEncoding.DecodeString(zipfile_entry_encoded_bytes)
		if err != nil {
			return empty_response, err
		}

		if zipped_file_filter(zipfile_entry_filename) {
			result[zipfile_entry_filename] = decoded_bytes
		}
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

// deletes a cache entry from the cache directory using the given `cache_key`.
func remove_cache_entry(cache_key string) error {
	return os.Remove(cache_path(cache_key))
}

// returns true if the given `path` hasn't been modified for a certain duration.
// different paths have different durations.
// assumes `path` exists.
// returns `true` when an error occurs stat'ing `path`.
func cache_expired(path string) bool {
	if STATE.Flags.ScrapeCommand.UseExpiredCache {
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
		cache_duration_hrs = CACHE_DURATION_RELEASE_JSON
	default:
		cache_duration_hrs = CACHE_DURATION
	}

	if cache_duration_hrs == -1 {
		return false // cache at given `path` never expires
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

	take_http_token()
	defer release_http_token()

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

func user_agent() string {
	return fmt.Sprintf("github-wow-addon-catalogue-go/%v (%v)", APP_VERSION, APP_LOC)
}

func download(url string, headers map[string]string) (ResponseWrapper, error) {
	slog.Debug("HTTP GET", "url", url)
	empty_response := ResponseWrapper{}

	// ---

	req, err := http.NewRequestWithContext(trace_context(), http.MethodGet, url, nil)
	if err != nil {
		return empty_response, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", user_agent())

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

// returns a map of zipped-filename => uncompressed-bytes of files within a zipfile at `url`
// whose filenames match `zipped_file_filter`.
func download_zip(url string, headers map[string]string, zipped_file_filter func(string) bool) (map[string][]byte, error) {

	slog.Debug("HTTP GET .zip", "url", url)

	empty_response := map[string][]byte{}

	req, err := http.NewRequestWithContext(trace_context(), http.MethodGet, url, nil)
	if err != nil {
		return empty_response, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", user_agent())

	for header, header_val := range headers {
		req.Header.Set(header, header_val)
	}

	// ---

	cache_key := make_cache_key(req)
	cached_zip_file, err := read_zip_cache_entry(cache_key, zipped_file_filter)
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
			defer fh.Close()

			bl, err := io.ReadAll(fh)
			if err != nil {
				// again, file is probably busted, abort.
				return empty_response, fmt.Errorf("failed to read zip file entry: %w", err)
			}

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

	slog.Error("failed to download url after a number of attempts", "url", url, "num-attempts", num_attempts, "last-resp", resp.StatusCode)
	return ResponseWrapper{}, errors.New("failed to download url: " + url)
}

// ---

// simplified .toc file parsing.
// keys are lowercased.
// does not handle duplicate keys, last key wins.
func parse_toc_file(filename string, toc_bytes []byte) (map[string]string, error) {
	slog.Info("parsing .toc", "filename", filename)

	toc_bytes, err := elide_bom(toc_bytes)
	if err != nil {
		slog.Warn("failed detecting/eliding BOM in toc file", "filename", filename, "error", err)
	}

	line_list := strings.Split(strings.ReplaceAll(string(toc_bytes), "\r\n", "\n"), "\n")
	interesting_lines := map[string]string{}
	for _, line := range line_list {
		if strings.HasPrefix(line, "##") {
			bits := strings.SplitN(line, ":", 2)
			if len(bits) != 2 {
				slog.Debug("ignoring line in .toc file, key has no value", "filename", filename, "line", line)
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

// returns a regular expression that matches against any known flavor.
// ignores word boundaries.
func flavor_regexp() *regexp.Regexp {
	flavor_list := []string{}
	for _, flavor := range FLAVOR_LIST {
		flavor_list = append(flavor_list, string(flavor))
	}
	for flavor_alias := range FLAVOR_ALIAS_MAP {
		flavor_list = append(flavor_list, flavor_alias)
	}
	flavors := strings.Join(flavor_list, "|") // "mainline|wrath|somealias"
	pattern := fmt.Sprintf(`(?i)(?P<flavor>%s)`, flavors)
	return regexp.MustCompile(pattern)
}

// searches given string `v` for a game track.
// it's pretty unsophisticated, be careful.
// returns the flavor that was matched and it's canonical value.
func guess_game_track(v string) (string, Flavor) {
	matches := FLAVOR_REGEXP.FindStringSubmatch(v)

	if len(matches) == 2 {
		// "Foo-Vanilla" => [Foo-Vanilla Vanilla]
		flavor := strings.ToLower(matches[1])
		actual_flavor, is_alias := FLAVOR_ALIAS_MAP[flavor]
		if is_alias {
			return matches[1], actual_flavor
		}
		return matches[1], Flavor(flavor)
	}
	return "", ""
}

var FLAVOR_REGEXP = flavor_regexp()

// builds a regular expression to match .toc filenames and extract known flavors and aliases.
func toc_filename_regexp() *regexp.Regexp {
	flavor_list := []string{}
	for _, flavor := range FLAVOR_LIST {
		flavor_list = append(flavor_list, string(flavor))
	}
	for flavor_alias := range FLAVOR_ALIAS_MAP {
		flavor_list = append(flavor_list, flavor_alias)
	}
	flavors := strings.Join(flavor_list, "|") // "mainline|wrath|somealias"
	pattern := fmt.Sprintf(`(?i)^(?P<name>[\w!'-_. ]+?)(?:[-_](?P<flavor>%s))?\.toc$`, flavors)
	return regexp.MustCompile(pattern)
}

var TOC_FILENAME_REGEXP = toc_filename_regexp()

// parses the given `filename`,
// extracting the filename sans extension and any flavors,
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
		actual_flavor, is_alias := FLAVOR_ALIAS_MAP[flavor]
		if is_alias {
			return matches[1], actual_flavor
		}
		return matches[1], flavor
	}
	return "", ""
}

// parses the given `zip_file_entry` 'filename',
// that we expect to look like: 'AddonName/AddonName.toc' or 'AddonName/AddonName-flavor.toc',
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
	prefix, rest := bits[0], bits[1]             // "Bar/Bar.toc" => ["Bar", "Bar.toc"]
	filename, flavor := parse_toc_filename(rest) // "Bar.toc" => "Bar", "Bar-Wrath.toc" => "Bar", "wrath"
	slog.Debug("zip file entry", "name", zip_file_entry, "prefix", prefix, "rest", rest, "toc-match", filename, "flavor", flavor)
	if filename != "" {
		if prefix == filename {
			// perfect
			return true
		} else {
			if strings.EqualFold(prefix, filename) {
				// less perfect
				slog.Debug("mixed filename casing", "prefix", prefix, "filename", filename)
				return true
			}

			// edge cases: bundles, where a release contains multiple other addons but none it's own.
			// WOWRainbowUI/RainbowUI-Era
			// WOWRainbowUI/RainbowUI-Retail
			// won't fix

			// edge case: BetterZoneStats-v1.0/BetterZoneStats.toc
			// addon name has suffix '-v1.0' which doesn't match a .toc in `is_toc_file`.
			// improperly structured release, won't fix.

			// edge case: addon name contains flavour: "JadeUI-Classic/JadeUI-Classic.toc"
			// prefix: "JadeUI-Classic"
			// rest:   "JadeUI-Classic.toc"
			// filename: "JadeUI"
			// flavor: "classic"
			// shortcoming in my code, this hack helps
			prefix_match, prefix_flavor := guess_game_track(prefix)
			if prefix_match != "" && prefix_flavor == flavor {
				slog.Debug("edge case, addon name contains flavour", "name", zip_file_entry, "prefix", prefix, "rest", rest, "toc-match", filename, "flavor", flavor)
				return true
			}
		}
	}
	return false
}

// "30403" => "wrath"
func interface_number_to_flavor(interface_val string) (Flavor, error) {
	interface_int, err := strconv.Atoi(strings.TrimSpace(interface_val))
	if err != nil {
		return "", fmt.Errorf("failed to convert interface value to integer: %w", err)
	}

	idx := (interface_int / 1_00_00) * 1_00_00 // 12345 => 1 => 10000
	flavor, present := INTERFACE_RANGES[idx]
	if !present {
		return "", fmt.Errorf("interface value out of range: %d", interface_int)
	}
	return flavor, nil
}

// "100206, 40400, 11502" => [mainline, cata, vanilla]
func interface_value_to_flavor_list(interface_val string) ([]Flavor, error) {
	empty_response := []Flavor{}
	flavor_list := []Flavor{}
	for _, bit := range strings.Split(interface_val, ",") {
		flavor, err := interface_number_to_flavor(bit)
		if err != nil {
			// one (possibly all) values are bad. fail noisily.
			return empty_response, err
		}
		flavor_list = append(flavor_list, flavor)
	}
	return flavor_list, nil
}

// downloads a file asset from a Github release,
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
		slog.Warn("no .toc files found in .zip asset while extracting project ids", "url", asset_url)
	}

	project_id_list := []string{
		"x-curse-project-id",
		"x-wago-id",
		"x-wowi-id",
	}

	// so! what happens when an addon bundles another addon and we have multiple .toc files? which one is the correct one?
	// for example, `tullamods/Dominos` bundles `SFX-WoW/Masque_Dominos`.
	// we don't know :(
	// we can split the IDs we find into more and less likely and
	// we can issue a warning if we're still uncertain and stuck with multiple sets of IDs,
	// but ultimately we can't know.

	asset_name := regexp.MustCompile(`[\W_-]`).Split(filepath.Base(asset_url), 2)[0] // note: this only captures 'bar' in 'foo-bar.zip'
	keyval_idx := map[string]map[string]int{}
	project_id_map := map[string]string{}
	uncertain_project_id_map := map[string]string{}

	var project_id_map_ptr map[string]string
	for zipfile_entry, toc_bytes := range toc_file_map {
		// temporary, remove once cache cleaned up
		if !is_toc_file(zipfile_entry) {
			continue
		}

		filename := filepath.Base(zipfile_entry)
		if !strings.HasPrefix(filename, asset_name) {
			project_id_map_ptr = uncertain_project_id_map
		} else {
			project_id_map_ptr = project_id_map
		}

		keyvals, err := parse_toc_file(zipfile_entry, toc_bytes)
		if err != nil {
			return empty_response, fmt.Errorf("failed to parse .toc contents: %w", err)
		}
		for _, key := range project_id_list {
			val, present := keyvals[key]
			if present && val != "" && val != "0" {
				project_id_map_ptr[key] = val
				_, id_present := keyval_idx[key]
				if !id_present {
					keyval_idx[key] = map[string]int{}
				}
				keyval_idx[key][val] += 1
			}
		}
	}

	if len(project_id_map) > 0 {
		return project_id_map, nil
	}

	// issue a warning if we captured ID values from a .toc file that wasn't prefixed with the asset's filename,
	// and there are multiple distinct values for any one ID.
	// for example, if there are two wago IDs.
	if len(uncertain_project_id_map) > 0 && len(toc_file_map) > 1 {
		for _, pid := range project_id_list {
			if len(keyval_idx[pid]) > 1 {
				slog.Warn("project IDs for this project may not be correct", "id-idx", keyval_idx, "prefix", asset_name, "url", asset_url)
				break
			}
		}
	}

	return uncertain_project_id_map, nil
}

// extract the flavors from the filenames
// for 'flavorless' toc files,
// parse the file contents looking for interface versions
func extract_game_flavors_from_tocs(asset_list []GithubReleaseAsset) ([]Flavor, error) {
	flavor_list := []Flavor{}
	for _, asset := range asset_list {
		toc_file_map, err := github_zip_download(asset.BrowserDownloadURL, is_toc_file)
		if err != nil {
			slog.Error("failed to process remote zip file", "error", err)
			continue
		}

		if len(toc_file_map) == 0 {
			slog.Warn("no .toc files found in .zip asset while extracting game flavors", "url", asset.BrowserDownloadURL)
		}

		for toc_filename, toc_contents := range toc_file_map {
			// todo: remove this once cache cleaned up
			if !is_toc_file(toc_filename) {
				continue
			}

			_, flavor := parse_toc_filename(toc_filename)
			if flavor != "" {
				slog.Debug("found flavor in .toc filename, not inspecting .toc contents", "flavor", flavor, "url", asset.BrowserDownloadURL)
				flavor_list = append(flavor_list, flavor)
			} else {
				// 'flavorless', parse the toc contents
				slog.Debug("flavorless .toc file, inspecting .toc contents for flavor", "url", asset.BrowserDownloadURL)
				keyvals, err := parse_toc_file(toc_filename, toc_contents)
				if err != nil {
					// couldn't parse this .toc file for some reason, move on to next .toc file
					slog.Error("failed to parse zip file entry .toc contents", "contents", string(toc_contents), "error", err)
					continue
				}
				interface_value, present := keyvals["interface"]
				if !present {
					slog.Warn("no 'interface' value found in toc file", "filename", toc_filename, "asset", asset.BrowserDownloadURL)
				} else {
					flavors, err := interface_value_to_flavor_list(interface_value)
					if err != nil {
						slog.Error("failed to parse interface number to a flavor", "error", err)
						continue
					}
					slog.Debug("found flavor(s) in .toc file contents", "flavor", flavors)
					flavor_list = append(flavor_list, flavors...)
				}
			}
		}
	}

	return flavor_list, nil
}

func parse_release_dot_json(release_dot_json_bytes []byte) (*ReleaseDotJson, error) {

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

	var release_dot_json ReleaseDotJson
	err = json.Unmarshal(release_dot_json_bytes, &release_dot_json)
	if err != nil {
		return nil, fmt.Errorf("failed to parse release.json as JSON: %w", err)
	}

	// coerce game flavor values
	// works but is unnecessary. flavors pulled from the release.json are normalised before output.
	/*
		for i, entry := range release_dot_json.ReleaseJsonEntryList {
			for j, meta := range entry.Metadata {
				actual_flavor, is_alias := FLAVOR_ALIAS_MAP[meta.Flavor]
				if is_alias {
					meta.Flavor = actual_flavor
				}
				release_dot_json.ReleaseJsonEntryList[i].Metadata[j] = meta
			}
			release_dot_json.ReleaseJsonEntryList[i] = entry
		}
	*/

	// ... anything else?

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
func parse_repo(repo GithubRepo, page int) (Project, error) {
	slog.Info("parsing repo", "repo", repo.FullName)

	var empty_response Project

	per_page := 1
	releases_url := API_URL + fmt.Sprintf("/repos/%s/releases?page=%d&per_page=%d", repo.FullName, page, per_page)

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
	var release_dot_json *ReleaseDotJson
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
				// todo: if we return here then the addon is skipped entirely.
				// instead, should probably just ignore the release.json and move on.
				return empty_response, fmt.Errorf("failed to parse release.json: %w", err)
			}

			break
		}
	}

	flavor_list := []Flavor{}
	project_id_map := map[string]string{}

	if release_dot_json != nil {
		slog.Debug("release.json found", "repo", repo.FullName, "release", latest_github_release.Name)

		// ensure at least one release in 'releases' is available
		for _, entry := range release_dot_json.ReleaseJsonEntryList {
			for _, metadata := range entry.Metadata {
				flavor_list = append(flavor_list, metadata.Flavor)
			}
		}

		// find the matching asset
		// see `2072/Decursive` for a release.json using multiple releases
		first_release_dot_json_entry := release_dot_json.ReleaseJsonEntryList[0]
		for _, asset := range latest_github_release.AssetList {
			if asset.Name == first_release_dot_json_entry.Filename {
				project_id_map, err = extract_project_ids_from_toc_files(asset.BrowserDownloadURL)
				if err != nil {
					slog.Error("failed to extract project ids from release.json", "error", err)
				}
				break
			}
		}

	} else {

		// there is no release.json file,
		// look for .zip assets instead and try our luck.
		slog.Debug("no release.json found in latest release, looking for .zip file assets instead", "repo", repo.FullName)
		zip_file_asset_list := []GithubReleaseAsset{}
		for _, asset := range release_list[0].AssetList {
			if asset.ContentType == "application/zip" || asset.ContentType == "application/x-zip-compressed" {
				if strings.HasSuffix(asset.Name, ".zip") {
					zip_file_asset_list = append(zip_file_asset_list, asset)
				}
			}
		}

		if len(zip_file_asset_list) == 0 {
			return empty_response, ErrNoReleaseCandidateFound
		}

		// extract flavors ...
		flavor_list, err = extract_game_flavors_from_tocs(zip_file_asset_list)
		if err != nil {
			return empty_response, fmt.Errorf("failed to parse .toc files in assets")
		}

		for _, asset := range zip_file_asset_list {
			project_id_map, err = extract_project_ids_from_toc_files(asset.BrowserDownloadURL)
			if err != nil {
				slog.Error("failed to extract project ids", "error", err)
			}
			if len(project_id_map) != 0 {
				break
			}
		}
	}

	repo.ProjectIDMap = project_id_map
	flavor_list = unique_sorted_flavor_list(flavor_list)

	slog.Debug("found flavors", "flavor-list", flavor_list, "repo", repo.FullName)

	project := Project{
		GithubRepo:     repo,
		UpdatedDate:    latest_github_release.PublishedAtDate,
		FlavorList:     flavor_list,
		HasReleaseJSON: release_dot_json != nil,
		LastSeenDate:   &STATE.RunStart,
	}
	return project, nil
}

// parses many `GithubRepo` structs in to a list of `Project` structs.
// `GithubRepo` structs that fail to parse are excluded from the final list.
func parse_repo_list(repo_list []GithubRepo) []Project {
	var wg sync.WaitGroup
	project_chan := make(chan Project, 10)

	for _, repo := range repo_list {
		repo := repo
		wg.Add(1)
		go func() {
			defer wg.Done()
			release_number := 1 // if '2', then per_page=1 and page=2, etc
			project, err := parse_repo(repo, release_number)
			if err != nil {

				if errors.Is(err, ErrNoReleaseCandidateFound) {
					_, is_awkward := REPO_EXCEPTIONS[repo.FullName]
					if is_awkward {
						release_number = 2
						project, err = parse_repo(repo, 2)
					}
				}

				// we may have tried again. if there is still an error, fail as usual.
				if err != nil {
					if errors.Is(err, ErrNoReleasesFound) || errors.Is(err, ErrNoReleaseCandidateFound) {
						slog.Info("undownloadable addon, skipping", "repo", repo.FullName, "error", err)
						return
					}
					slog.Error("error parsing GithubRepo into a Project, skipping", "repo", repo.FullName, "error", err)
					return
				}
			}
			project_chan <- project
		}()
	}

	// close the chan when everything is done
	go func() {
		wg.Wait()
		close(project_chan)
	}()

	// accumulate the results while waiting for the channel to close.
	project_list := []Project{}
	for v := range project_chan {
		project_list = append(project_list, v)
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
	slog.Info("searching for repositories", "query", search_query)

	results := []string{} // blobs of json from github api
	per_page := 100
	search_query = url.QueryEscape(search_query)

	// sort and order the search results in different ways in an attempt to get at the addons not being returned.
	sort_list := []string{"created", "updated"} // note! these are *deprecated*.
	order_by_list := []string{"asc", "desc"}    // note! also *deprecated*.
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
	empty_response := GithubRepo{}

	// 'code' result, nested value, many missing fields
	repo_field := gjson.Get(search_result, "repository")
	var repo GithubRepo
	var err error
	if repo_field.Exists() {
		err = json.Unmarshal([]byte(repo_field.String()), &repo)
		if err != nil {
			slog.Error("failed to unmarshal 'code' search result to GithubRepo struct", "search-result", search_result, "error", err)
			return empty_response, err
		}
		return repo, nil
	}

	// 'repository' result
	err = json.Unmarshal([]byte(search_result), &repo)
	if err != nil {
		slog.Error("failed to unmarshal 'repository' search result to GithubRepo struct", "search-result", search_result, "error", err)
		return repo, err
	}

	return repo, nil
}

// convert a page of search results into a list of `GithubRepo` structs.
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

// a repository may be excluded because it is on a blacklist,
// or because it doesn't match a user provided `filter`.
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

func is_excluded_fn(blacklist map[string]bool, filter *regexp.Regexp) func(GithubRepo) bool {
	return func(repo GithubRepo) bool {
		_, b := is_excluded(blacklist, filter, repo.FullName)
		return b
	}
}

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
		//{"code", "user:curseforge-mirror"}, // github.com/curseforge-mirror
		{"code", "path:.github/workflows WOWI_API_TOKEN"},
		{"repositories", "topic:wow-addon"},
		{"repositories", "topic:world-of-warcraft-addon"},
		{"repositories", "topic:warcraft-addon"},
		{"repositories", "topics:>2 topic:world-of-warcraft topic:addon"},
	}

	// note: no real savings with async here, Github throttles searches heavily

	for _, pair := range search_list {
		endpoint, query := pair[0], pair[1]
		search_results := search_github(endpoint, query)
		for _, repo := range search_results_to_struct_list(search_results) {
			pattern, excluded := is_excluded(REPO_BLACKLIST, filter, repo.FullName)
			if excluded {
				_, present := WARNED[repo.FullName]
				if !present {
					slog.Debug("repository ignored", "repo", repo.FullName, "pattern", pattern)
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

	// todo: do we need this sort anymore?
	slices.SortFunc(struct_list, func(a, b GithubRepo) int {
		return strings.Compare(a.FullName, b.FullName)
	})

	return struct_list
}

// --- json i/o

// write a list of Projects as a JSON array to the given `output_file`,
// or to stdout if `output_file` is empty.
// JSON output is used for diffing and may not contain all the data present in CSV output.
func write_json(project_list []Project, output_file string) {
	modified_project_list := []Project{}
	for _, p := range project_list {
		p.LastSeenDate = nil
		modified_project_list = append(modified_project_list, p)
	}

	bytes, err := json.MarshalIndent(modified_project_list, "", "\t")
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
// not recommended. JSON output is used for diffing and may not contain all the data present in CSV output.
func read_json(path string) ([]GithubRepo, error) {
	var empty_response []GithubRepo
	fh, err := os.Open(path)
	if err != nil {
		return empty_response, fmt.Errorf("failed to open JSON file for reading: %w", err)
	}

	bytes, err := io.ReadAll(fh)
	if err != nil {
		return empty_response, fmt.Errorf("failed to read bytes in JSON file: %w", err)
	}

	project_list := []GithubRepo{}
	err = json.Unmarshal(bytes, &project_list)
	if err != nil {
		return empty_response, fmt.Errorf("failed to parse JSON in file: %w", err)
	}

	return project_list, nil
}

// --- csv i/o

// write a list of Projects as CSV to the given `output_file`,
// or to stdout if `output_file` is empty.
func write_csv(project_list []Project, output_file string) {
	output := os.Stdout
	if output_file != "" {
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
		writer.Write(project_to_csv_row(project))
	}
	writer.Flush()
}

// read a list of `GithubRepo` structs from the given CSV file at `path`.
// note: a row is a complete `Project` but we use a `GithubRepo` instead as all inputs *must* be parsed.
// CSV structure follows original.
func read_csv(path string) ([]GithubRepo, error) {
	empty_response := []GithubRepo{}
	fh, err := os.Open(path)
	if err != nil {
		return empty_response, fmt.Errorf("failed to open input file: %w", err)
	}
	defer fh.Close()
	repo_list := []GithubRepo{}
	rdr := csv.NewReader(fh)

	rdr.Read() // header

	row_list, err := rdr.ReadAll()
	if err != nil {
		return empty_response, fmt.Errorf("failed to read contents of input file: %w", err)
	}
	for _, row := range row_list {
		repo_list = append(repo_list, repo_from_csv_row(row))
	}

	return repo_list, nil
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

func usage() string {
	return "usage: ./github-wow-addon-catalogue <scrape|dump-release-dot-json|find-duplicates|cache-stats|cache-prune>"
}

func read_flags(arg_list []string) Flags {
	app_flags := Flags{}
	var flagset *flag.FlagSet

	defaults := flag.NewFlagSet("github-wow-addon-catalogue", flag.ContinueOnError)
	help_ptr := defaults.BoolP("help", "h", false, "print this help and exit")
	version_ptr := defaults.BoolP("version", "V", false, "print program version and exit")
	log_level_label_ptr := defaults.String("log-level", "info", "verbosity level. one of: debug, info, warn, error")

	input_file_list := []string{}

	//

	scrape_cmd := ScrapeCommand{}
	scrape_flagset := flag.NewFlagSet("scrape", flag.ExitOnError)
	scrape_flagset.StringArrayVar(&input_file_list, "in", []string{}, "path to extant addons.csv file. input is merged with search results")
	scrape_flagset.StringArrayVar(&scrape_cmd.OutputFileList, "out", []string{}, "write results to file and not stdout")
	scrape_flagset.BoolVar(&scrape_cmd.SkipSearch, "skip-search", false, "don't search Github")
	scrape_flagset.BoolVar(&scrape_cmd.UseExpiredCache, "use-expired-cache", false, "ignore whether a cached file has expired")
	scrape_flagset.StringVar(&scrape_cmd.FilterPattern, "filter", "", "limit catalogue to addons matching regex")

	//

	dump_release_dot_json_flagset := flag.NewFlagSet("release.json-dump", flag.ExitOnError)

	//

	find_dupes_cmd := FindDuplicatesCommand{}
	dupes_flagset := flag.NewFlagSet("find-duplicates", flag.ExitOnError)
	dupes_flagset.StringArrayVar(&input_file_list, "in", []string{}, "path to extant addons.csv file")

	//

	cache_stats_flagset := flag.NewFlagSet("cache-stats", flag.ExitOnError)

	//

	cache_prune_cmd := CachePruneCommand{}
	cache_prune_flagset := flag.NewFlagSet("cache-prune", flag.ExitOnError)
	cache_prune_flagset.StringVar(&cache_prune_cmd.DeleteFlag, "delete", "", "actually delete files when set to 'true' (allowed values: true, false; default is dry-run)")

	//

	// first arg is always the name of the running app.
	// second arg should be the subcommand but could be -h or -V
	var subcommand string
	if len(arg_list) > 1 {
		subcommand = arg_list[1]
	}

	switch subcommand {
	case ScrapeSubCommand:
		flagset = scrape_flagset
		flagset.AddFlagSet(defaults)

	case DumpReleaseDotJsonSubCommand:
		flagset = dump_release_dot_json_flagset
		flagset.AddFlagSet(defaults)

	case FindDuplicatesSubCommand:
		flagset = dupes_flagset
		flagset.AddFlagSet(defaults)

	case CacheStatsSubCommand:
		flagset = cache_stats_flagset
		flagset.AddFlagSet(defaults)

	case CachePruneSubCommand:
		flagset = cache_prune_flagset
		flagset.AddFlagSet(defaults)

	default:
		flagset = defaults
	}

	flagset.Parse(arg_list)

	if help_ptr != nil && *help_ptr {
		fmt.Println(usage())
		flagset.PrintDefaults()
		os.Exit(0)
	}

	if version_ptr != nil && *version_ptr {
		fmt.Println(APP_VERSION)
		os.Exit(0)
	}

	if subcommand == "" || !slices.Contains(KNOWN_SUBCOMMANDS, subcommand) {
		fmt.Println(usage())
		flagset.PrintDefaults()
		fatal()
	}

	for _, input_file := range input_file_list {
		die(!path_exists(input_file), fmt.Sprintf("input path does not exist: %s", input_file))
		ext := filepath.Ext(input_file)
		die(ext == "", fmt.Sprintf("input path has no extension: %s", input_file_list))
		die(ext != ".csv" && ext != ".json", fmt.Sprintf("input path has unsupported extension: %s", ext))
	}
	scrape_cmd.InputFileList = input_file_list
	find_dupes_cmd.InputFileList = input_file_list

	for _, output_file := range scrape_cmd.OutputFileList {
		ext := filepath.Ext(output_file)
		die(ext == "", fmt.Sprintf("output path has no extension: %s", scrape_cmd.OutputFileList))
		die(ext != ".csv" && ext != ".json", fmt.Sprintf("output path has unsupported extension: %s", ext))
	}

	die(scrape_cmd.SkipSearch && len(scrape_cmd.InputFileList) == 0, "cannot skip search if no input files provided")

	if scrape_cmd.FilterPattern != "" {
		pattern, err := regexp.Compile(scrape_cmd.FilterPattern)
		if err != nil {
			slog.Error(fmt.Sprintf("filter could not be compiled to a regular expression: %v", err.Error()))
			fatal()
		}
		scrape_cmd.FilterPatternRegexp = pattern
	}

	// Validate cache-prune --delete flag
	if subcommand == CachePruneSubCommand && cache_prune_cmd.DeleteFlag != "" {
		switch cache_prune_cmd.DeleteFlag {
		case "true":
			cache_prune_cmd.Delete = true
		case "false":
			cache_prune_cmd.Delete = false
		default:
			die(true, fmt.Sprintf("--delete flag requires explicit value 'true' or 'false', got: %s", cache_prune_cmd.DeleteFlag))
		}
	}

	log_level_label_map := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}
	log_level, present := log_level_label_map[*log_level_label_ptr]
	die(!present, fmt.Sprintf("unknown log level: %s", *log_level_label_ptr))
	app_flags.LogLevel = log_level

	//

	app_flags.SubCommand = subcommand
	app_flags.ScrapeCommand = scrape_cmd
	app_flags.FindDuplicatesCommand = find_dupes_cmd
	app_flags.CachePruneCommand = cache_prune_cmd

	return app_flags
}

func read_input_file_list(input_file_list []string, filter_fn func(GithubRepo) bool) ([]GithubRepo, error) {
	empty_response := []GithubRepo{}
	if len(input_file_list) == 0 {
		return empty_response, nil
	}

	slog.Info("reading addons from input file(s)", "path-list", input_file_list)
	input_repo_list := []GithubRepo{}
	for _, input_file := range input_file_list {
		ext := filepath.Ext(input_file)
		var repo_list []GithubRepo
		var err error

		switch ext {
		case ".csv":
			repo_list, err = read_csv(input_file)
		case ".json":
			repo_list, err = read_json(input_file)
		}
		if err != nil {
			return empty_response, err
		}
		slog.Info("found addons", "num", len(repo_list), "input-file", input_file, "filtered", STATE.Flags.ScrapeCommand.FilterPattern != "")
		input_repo_list = append(input_repo_list, repo_list...)
	}

	filtered_input_repo_list := []GithubRepo{}
	if filter_fn != nil {
		for _, repo := range input_repo_list {
			if !filter_fn(repo) {
				filtered_input_repo_list = append(filtered_input_repo_list, repo)
			}
		}
	} else {
		filtered_input_repo_list = input_repo_list
	}

	// todo: validate

	slog.Info("final addons", "num", len(filtered_input_repo_list), "num-input-files", len(STATE.Flags.ScrapeCommand.InputFileList), "filtered", STATE.Flags.ScrapeCommand.FilterPattern != "")
	return filtered_input_repo_list, nil
}

func unique_repo_list(github_repo_list []GithubRepo) []GithubRepo {
	if len(github_repo_list) == 1 {
		return github_repo_list
	}

	// de-duplicate repos with later inputs overriding earlier inputs.
	// for example, results in input file 1 are overridden by input file 2 that are overridden by search results.
	repo_idx := map[int]GithubRepo{}
	for _, repo := range github_repo_list {
		repo_idx[repo.ID] = repo
	}

	unique_github_repo_list := []GithubRepo{}
	for _, repo := range repo_idx {
		unique_github_repo_list = append(unique_github_repo_list, repo)
	}

	slog.Info("de-duplicated addons", "num", len(github_repo_list), "unique", len(unique_github_repo_list))

	// todo: do we need this sort any more?
	slices.SortFunc(unique_github_repo_list, func(a, b GithubRepo) int {
		return strings.Compare(a.FullName, b.FullName)
	})

	return unique_github_repo_list
}

func scrape() {
	if STATE.GithubToken == "" {
		slog.Error("Environment variable 'ADDONS_CATALOGUE_GITHUB_TOKEN' not set")
		fatal()
	}

	var err error
	var github_repo_list []GithubRepo

	filter_fn := is_excluded_fn(REPO_BLACKLIST, STATE.Flags.ScrapeCommand.FilterPatternRegexp)
	github_repo_list, err = read_input_file_list(STATE.Flags.ScrapeCommand.InputFileList, filter_fn)
	if err != nil {
		slog.Error("failed to read input file(s)", "error", err)
		fatal()
	}

	if !STATE.Flags.ScrapeCommand.SkipSearch {
		slog.Info("searching for addons")
		search_results := get_projects(STATE.Flags.ScrapeCommand.FilterPatternRegexp)
		slog.Info("found addons", "num", len(search_results))
		github_repo_list = append(github_repo_list, search_results...)
	}

	github_repo_list = unique_repo_list(github_repo_list)

	slog.Info("parsing addons")
	project_list := parse_repo_list(github_repo_list)
	slog.Info("addons parsed", "num", len(github_repo_list), "viable", len(project_list))

	slices.SortFunc(project_list, func(a, b Project) int {
		return strings.Compare(a.FullName, b.FullName)
	})

	if len(STATE.Flags.ScrapeCommand.OutputFileList) > 0 {
		for _, output_file := range STATE.Flags.ScrapeCommand.OutputFileList {
			ext := filepath.Ext(output_file)
			switch ext {
			case ".csv":
				write_csv(project_list, output_file)
			case ".json":
				write_json(project_list, output_file)
			}
			slog.Info(fmt.Sprintf("wrote %s file", ext), "output-file", output_file)
		}
	} else {
		write_json(project_list, "")
	}
}

// ---

// prints release.json files to stdout one per line.
// the output can be fed into the `ogri-la/release.json-validator` project.
func dump_release_dot_json() {
	jsonl := []map[string]any{}
	for _, cache_file := range cache_entry_list() {
		if strings.HasSuffix(cache_file, "-release.json") {
			resp, err := read_cache_entry(cache_file)
			if err != nil {
				slog.Warn("failed to reach cache file", "cache-file", cache_file, "error", err)
				continue
			}

			cache_file_bytes, err := io.ReadAll(resp.Body)
			if err != nil {
				slog.Warn("failed to read bytes of cache response", "cache-file", cache_file, "error", err)
				continue
			}

			// read json data into a simple struct
			var gen map[string]any
			err = json.Unmarshal(cache_file_bytes, &gen)
			if err != nil {
				slog.Warn("failed to unmarshal cached release.json bytes", "cache-file", cache_file, "error", err)
				continue
			}

			jsonl = append(jsonl, gen)
		}
	}

	for _, line := range jsonl {
		json_bytes, err := json.Marshal(line)
		if err != nil {
			slog.Warn("failed to marshal release.json data", "error", err)
			continue
		}
		fmt.Println(string(json_bytes))
	}
}

// ---

// find addons in input list of addons that share sources.
// a 'source' is a project ID like x-wowi-id or x-curse-project-id.
func find_duplicates() {
	github_repo_list, err := read_input_file_list(STATE.Flags.FindDuplicatesCommand.InputFileList, nil)
	if err != nil {
		slog.Error("failed to read input files", "error", err)
		fatal()
	}
	github_repo_list = unique_repo_list(github_repo_list)

	idx := map[string][]GithubRepo{}
	for _, repo := range github_repo_list {
		for key, val := range repo.ProjectIDMap {
			if val == "0" {
				// edge case, several addons seem to just set this to zero. ignore.
				// even though this has been fixed higher up, it may still be introduced via outside addons.csv
				continue
			}

			idx_key := key + ": " + val // x-wago-id: 1234567
			grp, present := idx[idx_key]
			if !present {
				grp = []GithubRepo{}
			}
			grp = append(grp, repo)
			idx[idx_key] = grp
		}
	}

	for key, repo_list := range idx {
		if len(repo_list) < 2 {
			continue
		}

		repo_name_list := []string{}   // ["foo/bar", "bar/baz"]
		owner_idx := map[string]bool{} // {"foo": ..., "bar": ...}
		for _, repo := range repo_list {
			repo_name_list = append(repo_name_list, repo.FullName)
			owner := strings.Split(repo.FullName, "/")[0] // ["foo/bar", "foo/baz"] => "foo"
			owner_idx[owner] = true
		}

		// ignore repo bundles that share the same owner
		if len(owner_idx) < 2 {
			continue
		}

		// ignore repo bundles that are known cases
		is_known_case := false
		for _, known_case := range KNOWN_DUPLICATE_LIST {
			if array_sorted_equal(repo_name_list, known_case) {
				is_known_case = true
				break
			}
		}
		if is_known_case {
			continue
		}

		fmt.Println(key)
		for i, repo := range repo_list {
			fmt.Printf("[%d] %s %v\n", i+1, repo.FullName, repo)
		}
		fmt.Println("---")
	}
}

// ---

type CacheFileStats struct {
	Count     int
	TotalSize int64
	MinSize   int64
	MaxSize   int64
	AvgSize   int64
	P95Size   int64
	MinAge    time.Duration
	MaxAge    time.Duration
	AvgAge    time.Duration
	Sizes     []int64         // for percentile calculation
	Ages      []time.Duration // for percentile calculation
}

func calculate_percentile(sorted_values []int64, percentile float64) int64 {
	if len(sorted_values) == 0 {
		return 0
	}
	index := int(float64(len(sorted_values)) * percentile)
	if index >= len(sorted_values) {
		index = len(sorted_values) - 1
	}
	return sorted_values[index]
}

func cache_stats() {
	cache_entries := cache_entry_list()
	if len(cache_entries) == 0 {
		fmt.Println("Cache is empty")
		return
	}

	// categorize cache entries by type
	stats_map := map[string]*CacheFileStats{
		"search":       {MinSize: math.MaxInt64, MinAge: time.Duration(math.MaxInt64), Sizes: []int64{}, Ages: []time.Duration{}},
		"zip":          {MinSize: math.MaxInt64, MinAge: time.Duration(math.MaxInt64), Sizes: []int64{}, Ages: []time.Duration{}},
		"release.json": {MinSize: math.MaxInt64, MinAge: time.Duration(math.MaxInt64), Sizes: []int64{}, Ages: []time.Duration{}},
		"other":        {MinSize: math.MaxInt64, MinAge: time.Duration(math.MaxInt64), Sizes: []int64{}, Ages: []time.Duration{}},
	}

	overall_stats := &CacheFileStats{
		MinSize: math.MaxInt64,
		MinAge:  time.Duration(math.MaxInt64),
		Sizes:   []int64{},
		Ages:    []time.Duration{},
	}

	for _, cache_file := range cache_entries {
		cache_file_path := cache_path(cache_file)
		info, err := os.Stat(cache_file_path)
		if err != nil {
			slog.Warn("failed to stat cache file", "cache-file", cache_file, "error", err)
			continue
		}

		// determine cache type
		var cache_type string
		if strings.HasSuffix(cache_file, "-search") {
			cache_type = "search"
		} else if strings.HasSuffix(cache_file, "-zip") {
			cache_type = "zip"
		} else if strings.HasSuffix(cache_file, "-release.json") {
			cache_type = "release.json"
		} else {
			cache_type = "other"
		}

		size := info.Size()
		age := STATE.RunStart.Sub(info.ModTime())

		// update type-specific stats
		stats := stats_map[cache_type]
		stats.Count++
		stats.TotalSize += size
		if size < stats.MinSize {
			stats.MinSize = size
		}
		if size > stats.MaxSize {
			stats.MaxSize = size
		}
		stats.Sizes = append(stats.Sizes, size)
		stats.Ages = append(stats.Ages, age)

		// running average: avg_new = avg_old + (value - avg_old) / n
		stats.AvgAge = stats.AvgAge + time.Duration((age.Nanoseconds()-stats.AvgAge.Nanoseconds())/int64(stats.Count))

		if age < stats.MinAge {
			stats.MinAge = age
		}
		if age > stats.MaxAge {
			stats.MaxAge = age
		}

		// update overall stats
		overall_stats.Count++
		overall_stats.TotalSize += size
		if size < overall_stats.MinSize {
			overall_stats.MinSize = size
		}
		if size > overall_stats.MaxSize {
			overall_stats.MaxSize = size
		}
		overall_stats.Sizes = append(overall_stats.Sizes, size)
		overall_stats.Ages = append(overall_stats.Ages, age)

		// running average: avg_new = avg_old + (value - avg_old) / n
		overall_stats.AvgAge = overall_stats.AvgAge + time.Duration((age.Nanoseconds()-overall_stats.AvgAge.Nanoseconds())/int64(overall_stats.Count))

		if age < overall_stats.MinAge {
			overall_stats.MinAge = age
		}
		if age > overall_stats.MaxAge {
			overall_stats.MaxAge = age
		}
	}

	// calculate size averages and percentiles (age average computed incrementally)
	for _, stats := range stats_map {
		if stats.Count > 0 {
			stats.AvgSize = stats.TotalSize / int64(stats.Count)

			// sort for percentile calculation
			slices.Sort(stats.Sizes)
			stats.P95Size = calculate_percentile(stats.Sizes, 0.95)
		} else {
			stats.MinSize = 0
		}
	}

	if overall_stats.Count > 0 {
		overall_stats.AvgSize = overall_stats.TotalSize / int64(overall_stats.Count)

		slices.Sort(overall_stats.Sizes)
		overall_stats.P95Size = calculate_percentile(overall_stats.Sizes, 0.95)
	}

	// print results
	fmt.Println("Cache Statistics")
	fmt.Println("================")
	fmt.Println()

	// overall stats
	fmt.Println("Overall:")
	fmt.Printf("  Files:       %d\n", overall_stats.Count)
	fmt.Printf("  Total Size:  %s (%d bytes)\n", format_bytes(overall_stats.TotalSize), overall_stats.TotalSize)
	fmt.Printf("  Min Size:    %s\n", format_bytes(overall_stats.MinSize))
	fmt.Printf("  Max Size:    %s\n", format_bytes(overall_stats.MaxSize))
	fmt.Printf("  Avg Size:    %s\n", format_bytes(overall_stats.AvgSize))
	fmt.Printf("  95th pct:    %s\n", format_bytes(overall_stats.P95Size))
	fmt.Printf("  Min Age:     %s\n", format_duration(overall_stats.MinAge))
	fmt.Printf("  Max Age:     %s\n", format_duration(overall_stats.MaxAge))
	fmt.Printf("  Avg Age:     %s\n", format_duration(overall_stats.AvgAge))
	fmt.Println()

	// type-specific stats
	type_order := []string{"search", "release.json", "zip", "other"}
	for _, cache_type := range type_order {
		stats := stats_map[cache_type]
		if stats.Count == 0 {
			continue
		}

		fmt.Printf("%s:\n", title_case(cache_type))
		fmt.Printf("  Files:       %d\n", stats.Count)
		fmt.Printf("  Total Size:  %s (%d bytes)\n", format_bytes(stats.TotalSize), stats.TotalSize)
		fmt.Printf("  Min Size:    %s\n", format_bytes(stats.MinSize))
		fmt.Printf("  Max Size:    %s\n", format_bytes(stats.MaxSize))
		fmt.Printf("  Avg Size:    %s\n", format_bytes(stats.AvgSize))
		fmt.Printf("  95th pct:    %s\n", format_bytes(stats.P95Size))
		fmt.Printf("  Min Age:     %s\n", format_duration(stats.MinAge))
		fmt.Printf("  Max Age:     %s\n", format_duration(stats.MaxAge))
		fmt.Printf("  Avg Age:     %s\n", format_duration(stats.AvgAge))
		fmt.Println()
	}
}

func format_bytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func format_duration(d time.Duration) string {
	if d < 0 {
		return fmt.Sprintf("-%s", format_duration(-d))
	}
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%.1fm", d.Minutes())
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%.1fh", d.Hours())
	}
	return fmt.Sprintf("%.1fd", d.Hours()/24)
}

// ---

// cache_prune removes expired cache files according to their type-specific expiration settings.
// Cap -1 (never expire) to 365 days maximum. Delete 0-byte files regardless of expiry.
func cache_prune() {
	cache_entries := cache_entry_list()
	if len(cache_entries) == 0 {
		fmt.Println("Cache is empty")
		return
	}

	dry_run := !STATE.Flags.CachePruneCommand.Delete
	max_age_days := 365 // cap "never expire" to 365 days

	if dry_run {
		fmt.Println("DRY RUN - files that would be deleted:")
		fmt.Println()
	} else {
		fmt.Println("Deleting expired cache files...")
		fmt.Println()
	}

	removed_count := 0
	removed_size := int64(0)
	kept_count := 0

	for _, cache_file := range cache_entries {
		cache_file_path := cache_path(cache_file)
		info, err := os.Stat(cache_file_path)
		if err != nil {
			slog.Warn("failed to stat cache file", "cache-file", cache_file, "error", err)
			continue
		}

		size := info.Size()
		age := STATE.RunStart.Sub(info.ModTime())

		// Always remove 0-byte files
		if size == 0 {
			if dry_run {
				fmt.Printf("  %s (0 bytes, %s old) - zero-byte file\n", cache_file, format_duration(age))
			} else {
				err := remove_cache_entry(cache_file)
				if err != nil {
					slog.Warn("failed to remove cache file", "cache-file", cache_file, "error", err)
				} else {
					slog.Debug("removed zero-byte cache file", "cache-file", cache_file)
				}
			}
			removed_count++
			removed_size += size
			continue
		}

		// Determine cache type and expiration
		var cache_type string
		var expiry_hours int

		bits := strings.Split(filepath.Base(cache_file_path), "-")
		suffix := ""
		if len(bits) == 2 {
			suffix = bits[1]
		}

		switch suffix {
		case "search":
			cache_type = "search"
			expiry_hours = CACHE_DURATION_SEARCH
		case "zip":
			cache_type = "zip"
			expiry_hours = CACHE_DURATION_ZIP
		case "release.json":
			cache_type = "release.json"
			expiry_hours = CACHE_DURATION_RELEASE_JSON
		default:
			cache_type = "other"
			expiry_hours = CACHE_DURATION
		}

		// Cap "never expire" (-1) to max_age_days
		if expiry_hours == -1 {
			expiry_hours = max_age_days * 24
		}

		// Check if expired
		age_hours := int(math.Floor(age.Hours()))
		expired := age_hours >= expiry_hours

		if expired {
			if dry_run {
				fmt.Printf("  %s (%s, %s old, type: %s, expires: %dh)\n",
					cache_file, format_bytes(size), format_duration(age), cache_type, expiry_hours)
			} else {
				err := remove_cache_entry(cache_file)
				if err != nil {
					slog.Warn("failed to remove cache file", "cache-file", cache_file, "error", err)
				} else {
					slog.Debug("removed expired cache file", "cache-file", cache_file, "type", cache_type, "age", age)
				}
			}
			removed_count++
			removed_size += size
		} else {
			kept_count++
		}
	}

	fmt.Println()
	if dry_run {
		fmt.Printf("Would remove: %d files (%s)\n", removed_count, format_bytes(removed_size))
		fmt.Printf("Would keep:   %d files\n", kept_count)
		fmt.Println()
		fmt.Println("Run with --delete to actually delete these files")
	} else {
		fmt.Printf("Removed: %d files (%s)\n", removed_count, format_bytes(removed_size))
		fmt.Printf("Kept:    %d files\n", kept_count)
	}
}

// ---

func init_state() *State {
	state := &State{
		RunStart: time.Now().UTC(),
	}

	cwd, err := os.Getwd()
	if err != nil {
		slog.Error("couldn't find the current working dir to set a writable location", "error", err)
		fatal()
	}
	state.CWD = cwd

	// attach a HTTP client to global state to reuse HTTP connections
	state.Client = &http.Client{}
	state.Client.Transport = &FileCachingRequest{}

	state.Schema = configure_validator()

	return state
}

func init() {
	if is_testing() {
		return
	}

	STATE = init_state()
	STATE.Flags = read_flags(os.Args)
	slog.SetDefault(slog.New(tint.NewHandler(os.Stderr, &tint.Options{Level: STATE.Flags.LogLevel})))

	token, _ := os.LookupEnv("ADDONS_CATALOGUE_GITHUB_TOKEN")
	STATE.GithubToken = token
}

func main() {
	switch STATE.Flags.SubCommand {
	case ScrapeSubCommand:
		scrape()

	case DumpReleaseDotJsonSubCommand:
		dump_release_dot_json()

	case FindDuplicatesSubCommand:
		find_duplicates()

	case CacheStatsSubCommand:
		cache_stats()

	case CachePruneSubCommand:
		cache_prune()
	}
}
