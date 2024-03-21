package main

import (
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"math"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lmittmann/tint"
	"github.com/tidwall/gjson"
)

type State struct {
	CWD         string
	GithubToken string
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
	Flavor    string `json:"flavor"`
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
	GameTrackList []string
	Label         string
	Name          string
	Source        string
	SourceId      string
	TagList       []string
	UpdatedDate   string
	URL           string
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
		log.Fatal("failed to coerce to json: ", err)
	}
	return string(b)
}

// calls `defer()`'s before exiting
func exit(code int) {
	runtime.Goexit()
}

func pprint(thing any) {
	s, _ := json.MarshalIndent(thing, "", "\t")
	fmt.Println(string(s))
}

func debug(msg string) {
	println(msg)
}

func warn(msg string, err error) {
	println(msg + ": " + err.Error())
}

func fatal(msg string, err error) {
	warn(msg, err)
	exit(1)
}

// int-to-string
func i2s(i int) string {
	return strconv.Itoa(i)
}

func trim(s string) string {
	return strings.TrimSpace(s)
}

// read text file at given `path`, returning contents as a string stripped of whitespace.
func slurp(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatal("failed to read file: " + path)
	}
	return trim(string(data))
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
		if resp.StatusCode > 399 {
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

func download(url string, headers map[string]string) (ResponseWrapper, error) {
	slog.Debug("HTTP GET", "url", url)
	empty_response := ResponseWrapper{}

	// fetch it

	client := http.Client{}
	customTransport := FileCachingRequest{}
	client.Transport = &customTransport

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fatal("error creating request", err)
	}
	for header, header_val := range headers {
		req.Header.Set(header, header_val)
	}
	resp, err := client.Do(req)
	if err != nil {
		warn("failed to fetch URL "+url, err)
		return empty_response, err
	}
	defer resp.Body.Close()

	// construct a response

	content_bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		warn("failed to read response body.", err)
		return empty_response, err
	}

	return ResponseWrapper{
		Response: resp,
		Text:     string(content_bytes),
	}, nil
}

// just like `download` but adds an 'authorization' header to the request.
func github_download(url string) (ResponseWrapper, error) {
	headers := map[string]string{"Authorization": "token " + STATE.GithubToken}
	return download(url, headers)
}

// ---

// inspects http response and determines if it was throttled.
func throttled(resp ResponseWrapper) bool {
	if resp.StatusCode == 422 || resp.StatusCode == 403 {
		debug("throttled")
		return true
	}
	return false
}

// inspects http response and determines how long to wait. then waits.
func wait(resp ResponseWrapper) {
	// TODO: something a bit cleverer than this.
	debug("waiting 5secs")
	time.Sleep(time.Duration(5) * time.Second)
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
				debug(fmt.Sprintf("attempt %d", i))
			}
			resp, err = github_download(api_url)
			if err != nil {
				log.Fatal("error requesting url: "+api_url, err)
			}

			if throttled(resp) {
				wait(resp)
				continue
			}

			if resp.StatusCode != 200 {
				fmt.Printf("non 200, non 422 response, waiting and trying again: %d", resp.StatusCode)
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
			exit(1)
		}

		for _, item := range item_list.Array() {
			g, err := json_string_to_struct(item.String())

			if g.Name == "" {
				fmt.Println("bad github repo")
				exit(1)
			}

			if err != nil {
				fmt.Println(err.Error())
				exit(1)
			}
			results_acc = append(results_acc, g)
		}
	}
	return results_acc
}

// downloads a release asset from github that matches the first entry in a release.json file.
func extract_project_ids_from_toc_files(asset_url string) {
    panic("not implemented")
}

// --- tasks

func parse_repo(repo GithubRepo) (Project, error) {

	slog.Info("parsing project", "project", repo.FullName)

	url := API_URL + fmt.Sprintf("/repos/%s/releases?per_page=1", repo.FullName)
	for {
		// fetch current release, if any
		resp, err := github_download_with_retries_and_backoff(url)
		if err != nil {
			//slog.Error("error downloading repository release listing", "error", err.Error())
			return Project{}, fmt.Errorf("failed to download repository release listing: %w", err)
		}

		var release_list []GithubRelease
		err = json.Unmarshal([]byte(resp.Text), &release_list)
		if err != nil {
			//slog.Error("error parsing Github 'release' response as JSON", "error", err)
			return Project{}, fmt.Errorf("failed to parse repository release listing as JSON: %w", err)
		}

		if len(release_list) != 1 {
			return Project{}, fmt.Errorf("project has no releases")
		}

		first_github_release := release_list[0] // 'release_json_release'

		pprint(first_github_release)

		var release_json_file *ReleaseJson
		for _, asset := range first_github_release.AssetList {
			if asset.Name == "release.json" {
				asset_resp, err := github_download_with_retries_and_backoff(asset.BrowserDownloadURL)
				if err != nil {
					return Project{}, fmt.Errorf("failed to download release.json: %w", err)
				}
				err = json.Unmarshal([]byte(asset_resp.Text), &release_json_file)
				if err != nil {
					return Project{}, fmt.Errorf("failed to parse release.json as JSON: %w", err)
				}
				break
			}
		}

		if release_json_file != nil {

			// todo: validate
			// ensure at least one release in 'releases' is available
			pprint(release_json_file)

			flavors := map[string]bool{}
			for _, entry := range release_json_file.ReleaseJsonEntryList {
				for _, metadata := range entry.Metadata {
					flavors[metadata.Flavor] = true
				}
			}

			// find the matching asset
			first_release_json_entry := release_json_file.ReleaseJsonEntryList[0]
			for _, asset := range first_github_release.AssetList {

				slog.Debug("match?", "asset-name", asset.Name, "release-name", first_github_release.Name)

				if asset.Name == first_release_json_entry.Filename {
					// downloads asset?
					//extract_project_ids_from_toc_files(get(asset.BrowserDownloadURL))

					slog.Info("found matching asset", "asset", asset)
					extract_project_ids_from_toc_files(asset.BrowserDownloadURL)

					break
				}
			}

		} else {

			// look for candidate assets
			slog.Info("looking for candidate assets")
			release_archives := []Asset{}
			for _, asset := range release_list[0].AssetList {
				pprint(asset)
				if asset.ContentType == "application/zip" || asset.ContentType == "application/x-zip-compressed" {
					if strings.HasSuffix(asset.Name, ".zip") {
						release_archives = append(release_archives, asset)
					}
				}
			}

			if len(release_archives) == 0 {
				return Project{}, fmt.Errorf("failed to find a release.json file or a downloadable addon from the assets")
			}

			// extract flavors ...
		}

		//println(resp.Text)
		exit(1)
		// look for "release.json" in release assets
		// if found, fetch it, validate it as json, validate as correct release-json (schema?)
		// for each asset in release, 'extract project ids from toc files'
		// this seems to involve reading the toc files inside zip files looking for "curse_id", "wago_id", "wowi_id" properties
		// a lot of toc data is just being ignored here :( and those properties are kind of rare

		// if not found, do the same as above, but for *all* zip files (not just those specified in release.json)

		// return a Project struct

		return Project{}, nil
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
		fmt.Println("Environment variable 'ADDONS_CATALOGUE_GITHUB_TOKEN' not present.")
		exit(1)
	}
	state.GithubToken = token

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	state.CWD = cwd

	return state
}

// --- bootstrap

func init() {
	STATE = init_state()
	slog.SetDefault(slog.New(tint.NewHandler(os.Stderr, &tint.Options{Level: slog.LevelDebug})))
}

func main() {
	defer os.Exit(0)
	slog.Info("searching for projects")
	github_repo_list := get_projects()
	slog.Info("found projects", "num", len(github_repo_list))

	slog.Info("parsing projects")
	project_list := parse_repo_list(github_repo_list)
	slog.Info("projects parsed", "viable", len(project_list))
}
