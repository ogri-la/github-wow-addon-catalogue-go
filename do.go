package main

import (
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

type State struct {
	CWD         string
	GithubToken string
}

type GithubRepoOwner struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

// what we get from Github
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
	License         string          `json:"license"`
	Topics          []string        `json:"topics"`
}

// what we'll render out
type CatalogueRepo struct {
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

// -- globals

var STATE State

var API_URL = "https://api.github.com"

// --- utils

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
	os.Exit(1)
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

func (x FileCachingRequest) RoundTrip(r *http.Request) (*http.Response, error) {
	cache_key := MakeCacheKey(r)
	// "/current/working/dir/output/711f20df1f76da140218e51445a6fc47"
	cache_path := CachePath(cache_key)
	cached_resp, err := ReadCacheEntry(cache_key)
	if err != nil {
		warn("http cache MISS", err)

		resp, err := http.DefaultTransport.RoundTrip(r)
		if err != nil {
			// do not cache error response, pass through
			return resp, err
		}

		if resp.StatusCode != 200 {
			// non-200 response, pass through
			return resp, nil
		}

		fh, err := os.Create(cache_path)
		if err != nil {
			warn("failed to open cache file for writing", err)
			return resp, nil
		}

		dumped_bytes, err := httputil.DumpResponse(resp, true)
		if err != nil {
			warn("failed to dump response to bytes", err)
			return resp, nil
		}

		_, err = fh.Write(dumped_bytes)
		if err != nil {
			warn("failed to write all bytes in response to cache file", err)
			fh.Close()
			return resp, nil
		}
		fh.Close()

		cached_resp, err = ReadCacheEntry(cache_key)
		if err != nil {
			warn("failed to read cache file", err)
			return resp, err
		}
		return cached_resp, nil

	} else {
		debug("http cache HIT " + cache_path)
		return cached_resp, nil
	}

	return http.DefaultTransport.RoundTrip(r)
}

type ResponseWrapper struct {
	*http.Response
	Text string
	JSON string
}

func download(url string, headers map[string]string) (ResponseWrapper, error) {
	debug("GET " + url)
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

func github_download(url string) (ResponseWrapper, error) {
	headers := map[string]string{"Authorization": "token " + STATE.GithubToken}
	return download(url, headers)
}

// ---

// inspects `resp` and determines if we were throttled.
func throttled(resp ResponseWrapper) bool {
	if resp.StatusCode == 422 || resp.StatusCode == 403 {
		debug("throttled")
		return true
	}
	return false
}

// inspects http response and determines how long to wait. then waits.
func wait(resp ResponseWrapper) {
	debug("waiting 30secs")
	time.Sleep(time.Duration(30) * time.Second)
}

// TODO: replace this with extracting the 'next' url from the `Link` header:
// Link: <https://api.github.com/search/code?q=path%3A.github%2Fworkflows+bigwigsmods+packager&per_page=100&page=8>; rel="prev", <https://api.github.com/search/code?q=path%3A.github%2Fworkflows+bigwigsmods+packager&per_page=100&page=10>; rel="next", <https://api.github.com/search/code?q=path%3A.github%2Fworkflows+bigwigsmods+packager&per_page=100&page=10>; rel="last", <https://api.github.com/search/code?q=path%3A.github%2Fworkflows+bigwigsmods+packager&per_page=100&page=1>; rel="first"
// inspects `resp` and determines if there are more pages to fetch.
func more_pages(page, per_page int, jsonstr string) int {
	val := gjson.Get(jsonstr, "total_count")
	total := int(val.Int())
	ptr := page * per_page                              // 300
	pos := total - ptr                                  // 743 - 300 = 443
	remaining_pages := float64(pos) / float64(per_page) // 4.43
	return int(math.Ceil(remaining_pages))              // 5
}

func search_github(endpoint string, query string) []string {
	results_acc := []string{}
	per_page := 100
	num_attempts := 5 // number of attempts to download the URL once throttled.
	page := 1
	query = url.PathEscape(query)

	for {
		var resp ResponseWrapper
		var err error
		api_url := API_URL + fmt.Sprintf("/search/%s?q=%s&per_page=%d&page=%d", endpoint, query, per_page, page)
		for i := 1; i <= num_attempts; i++ {
			debug(fmt.Sprintf("attempt %d", i))
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

			results_acc = append(results_acc, resp.Text)
			break
		}
		remaining_pages := more_pages(page, per_page, resp.Text)
		if remaining_pages > 0 && page < 10 {
			debug(fmt.Sprintf("remaining pages: %d", remaining_pages))
			page = page + 1
			continue
		}
		break
	}
	return results_acc
}

func json_string_to_struct(json_blob string) GithubRepo {

	// this is a 'code' result, many missing fields.
	repo := gjson.Get(json_blob, "repository").String()
	if repo != "" {
		json_blob = repo
	}
	g := GithubRepo{}
	json.Unmarshal([]byte(json_blob), &g)
	return g
}

func search_results_to_struct_list(search_results []string) []GithubRepo {
	results_acc := []GithubRepo{}
	for _, json_blob := range search_results {
		item_list := gjson.Get(json_blob, "items").Array()
		for _, item := range item_list {

			g := json_string_to_struct(item.String())
			results_acc = append(results_acc, g)
		}
	}
	return results_acc
}

// --- bootstrap

func init_state() State {
	s := State{}
	s.GithubToken = slurp("github-token")
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	s.CWD = cwd
	return s
}

func main() {
	STATE = init_state()
	struct_map := map[string]GithubRepo{}
	search_list := [][]string{
		// order is important.
		// duplicate 'code' results are replaced by by 'repositories' results
		[]string{"code", "path:.github/workflows bigwigsmods packager"},
		[]string{"code", "path:.github/workflows CF_API_KEY"},
		[]string{"repositories", "topic:wow-addon"},
		[]string{"repositories", "topics:>2 topic:world-of-warcraft topic:addon"}}
	for _, pair := range search_list {
		endpoint := pair[0]
		query := pair[1]
		search_results := search_github(endpoint, query)
		for _, repo := range search_results_to_struct_list(search_results) {
			struct_map[repo.FullName] = repo
		}
	}
	pprint(struct_map)
	println()
	pprint(len(struct_map))
}
