package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const defaultUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120 Safari/537.36"

// Client talks to an Arcadyan/Salt Fiber Box web UI.
type Client struct {
	Base string // e.g. http://192.168.1.1
	User string
	Pass string
	HTTP *http.Client
}

func NewClient(base, user, pass string) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &Client{
		Base: strings.TrimRight(base, "/"),
		User: user,
		Pass: pass,
		HTTP: &http.Client{
			Timeout: 20 * time.Second,
			Jar:     jar,
			// Do not auto-follow redirects: a 302 to /login.htm is our
			// signal that a request was unauthenticated.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (c *Client) get(path, referer string) (*http.Response, string, error) {
	req, _ := http.NewRequest("GET", c.Base+path, nil)
	req.Header.Set("User-Agent", defaultUA)
	if referer != "" {
		req.Header.Set("Referer", c.Base+"/"+strings.TrimLeft(referer, "/"))
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp, string(b), err
}

func (c *Client) post(path, referer string, form url.Values) (*http.Response, string, error) {
	req, _ := http.NewRequest("POST", c.Base+path, strings.NewReader(form.Encode()))
	req.Header.Set("User-Agent", defaultUA)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if referer != "" {
		req.Header.Set("Referer", c.Base+"/"+strings.TrimLeft(referer, "/"))
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp, string(b), err
}

// page loads an HTML page and returns its body plus the fresh per-load secrets.
func (c *Client) page(path string) (string, pageSecrets, error) {
	resp, body, err := c.get("/"+strings.TrimLeft(path, "/"), "")
	if err != nil {
		return "", pageSecrets{}, err
	}
	if resp.StatusCode == http.StatusFound {
		return "", pageSecrets{}, fmt.Errorf("not authenticated (got redirect to %s) — run `login` or check credentials", resp.Header.Get("Location"))
	}
	sec, err := scrapeSecrets(body)
	return body, sec, err
}

// Login performs the two-step login and leaves a valid SID cookie in the jar.
func (c *Client) Login() error {
	_, sec, err := c.page("login.htm")
	if err != nil {
		return fmt.Errorf("load login page: %w", err)
	}
	form := url.Values{
		"usr":     {credEncode(c.User)},
		"pws":     {credEncode(c.Pass)},
		"httoken": {sec.token},
	}
	resp, _, err := c.post("/login.cgi", "login.htm", form)
	if err != nil {
		return fmt.Errorf("post login: %w", err)
	}
	loc := resp.Header.Get("Location")
	switch {
	case strings.Contains(loc, "index.htm"):
		return nil // success
	case strings.Contains(loc, "err=1"):
		return fmt.Errorf("login rejected: wrong username or password")
	case strings.Contains(loc, "err=7"):
		return fmt.Errorf("login blocked: too many attempts, the box is temporarily locked (~60s)")
	default:
		return fmt.Errorf("login failed (location=%q, status=%d)", loc, resp.StatusCode)
	}
}

// GetCGI fetches a cgi/cgi_<name>.js data file using a fresh token obtained
// from the given referer page, and returns its raw JS body.
func (c *Client) GetCGI(name, referer string) (string, error) {
	_, sec, err := c.page(referer)
	if err != nil {
		return "", err
	}
	q := url.Values{"_tn": {sec.token}, "_t": {fmt.Sprint(time.Now().UnixMilli())}}
	resp, body, err := c.get("/cgi/cgi_"+name+".js?"+q.Encode(), referer)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cgi_%s.js: HTTP %d", name, resp.StatusCode)
	}
	return body, nil
}

// PostSimple posts a plaintext form (plus a fresh httoken) to an endpoint such
// as /owl_cmd.cgi. The LED control uses this path.
func (c *Client) PostSimple(endpoint, referer string, fields map[string]string) (string, error) {
	_, sec, err := c.page(referer)
	if err != nil {
		return "", err
	}
	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}
	form.Set("httoken", sec.token)
	_, body, err := c.post("/"+strings.TrimLeft(endpoint, "/"), referer, form)
	return body, err
}

// Apply posts a settings change to /apply_abstract.cgi the way the UI's
// genForm does: action = AES(cmd), each field value AES-encoded, plus httoken
// and submit_button = the current page.
func (c *Client) Apply(cmd, page string, fields map[string]string) (string, error) {
	_, sec, err := c.page(page)
	if err != nil {
		return "", err
	}
	action, err := aesEncode(sec, cmd)
	if err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("action", action)
	form.Set("httoken", sec.token)
	form.Set("submit_button", page)
	for k, v := range fields {
		enc, err := aesEncode(sec, v)
		if err != nil {
			return "", err
		}
		form.Set(k, enc)
	}
	_, body, err := c.post("/apply_abstract.cgi", page, form)
	return body, err
}

// PageData loads a page and every cgi data file it declares, returning the
// merged editable Config (addCfg entries) and, per cgi, the read-only vars.
func (c *Client) PageData(page string) (*Config, map[string][]kv, error) {
	body, _, err := c.page(page)
	if err != nil {
		return nil, nil, err
	}
	cfg := &Config{byName: map[string]int{}}
	reads := map[string][]kv{}
	for _, name := range pageCGIs(body) {
		js, err := c.GetCGI(name, page)
		if err != nil {
			reads[name] = []kv{{"(error)", err.Error()}}
			continue
		}
		for _, f := range parseConfig(js).Fields {
			if _, dup := cfg.byName[f.Name]; !dup {
				cfg.byName[f.Name] = len(cfg.Fields)
				cfg.Fields = append(cfg.Fields, f)
			}
		}
		reads[name] = parseVars(js)
	}
	return cfg, reads, nil
}

// --- small parsers for the firmware's JS data files ---

var (
	cgisListRe = regexp.MustCompile(`var\s+CGIs\s*=\s*\[([^\]]*)\]`)
	cgisPushRe = regexp.MustCompile(`CGIs\.push\(\s*"([^"]+)"\s*\)`)
	strItemRe  = regexp.MustCompile(`"([^"]*)"`)
	varRe      = regexp.MustCompile(`var\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"((?:[^"\\]|\\.)*)"`)
)

// pageCGIs returns the cgi data-file base names a page declares.
func pageCGIs(html string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if m := cgisListRe.FindStringSubmatch(html); m != nil {
		for _, it := range strItemRe.FindAllStringSubmatch(m[1], -1) {
			add(it[1])
		}
	}
	for _, m := range cgisPushRe.FindAllStringSubmatch(html, -1) {
		add(m[1])
	}
	return out
}

type kv struct{ Key, Val string }

// parseVars extracts top-level `var NAME="...";` string assignments.
func parseVars(js string) []kv {
	var out []kv
	for _, m := range varRe.FindAllStringSubmatch(js, -1) {
		out = append(out, kv{m[1], m[2]})
	}
	return out
}
