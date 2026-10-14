package arcadyan

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

const defaultUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120 Safari/537.36"

// Client is an authenticated session to an Arcadyan WebBase admin UI.
type Client struct {
	Base string
	User string
	Pass string
	HTTP *http.Client
}

func New(base, user, pass string) (*Client, error) {
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
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse // a 302 to /login.htm means "unauthenticated"
			},
		},
	}, nil
}

func (c *Client) do(method, path, referer string, body io.Reader, ct string) (*http.Response, string, error) {
	req, _ := http.NewRequest(method, c.Base+path, body)
	req.Header.Set("User-Agent", defaultUA)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
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

// Page loads an HTML page and returns its body plus the fresh per-load secrets.
func (c *Client) Page(path string) (string, Secrets, error) {
	resp, body, err := c.do("GET", "/"+strings.TrimLeft(path, "/"), "", nil, "")
	if err != nil {
		return "", Secrets{}, err
	}
	if resp.StatusCode == http.StatusFound {
		return "", Secrets{}, fmt.Errorf("not authenticated (redirect to %s) — check credentials", resp.Header.Get("Location"))
	}
	if resp.StatusCode == http.StatusForbidden {
		return "", Secrets{}, fmt.Errorf("%s: HTTP 403 (needs a higher-privilege account)", path)
	}
	sec, err := ScrapeSecrets(body)
	return body, sec, err
}

// Login performs the two-step login, leaving a valid SID cookie in the jar.
func (c *Client) Login() error {
	_, sec, err := c.Page("login.htm")
	if err != nil {
		return fmt.Errorf("load login page: %w", err)
	}
	form := url.Values{"usr": {CredEncode(c.User)}, "pws": {CredEncode(c.Pass)}, "httoken": {sec.Token}}
	resp, _, err := c.do("POST", "/login.cgi", "login.htm", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	if err != nil {
		return fmt.Errorf("post login: %w", err)
	}
	loc := resp.Header.Get("Location")
	switch {
	case strings.Contains(loc, "index.htm"):
		return nil
	case strings.Contains(loc, "err=1"):
		return fmt.Errorf("login rejected: wrong username or password")
	case strings.Contains(loc, "err=7"):
		return fmt.Errorf("login blocked: too many attempts, box locked ~60s")
	default:
		return fmt.Errorf("login failed (location=%q, status=%d)", loc, resp.StatusCode)
	}
}

// GetCGI fetches a cgi/cgi_<name>.js data file with a fresh token from referer.
func (c *Client) GetCGI(name, referer string) (string, error) {
	_, sec, err := c.Page(referer)
	if err != nil {
		return "", err
	}
	q := url.Values{"_tn": {sec.Token}, "_t": {fmt.Sprint(time.Now().UnixMilli())}}
	resp, body, err := c.do("GET", "/cgi/cgi_"+name+".js?"+q.Encode(), referer, nil, "")
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cgi_%s.js: HTTP %d", name, resp.StatusCode)
	}
	return body, nil
}

// PostSimple posts a plaintext form (+ fresh httoken) to e.g. /owl_cmd.cgi.
func (c *Client) PostSimple(endpoint, referer string, fields map[string]string) (string, error) {
	_, sec, err := c.Page(referer)
	if err != nil {
		return "", err
	}
	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}
	form.Set("httoken", sec.Token)
	_, body, err := c.do("POST", "/"+strings.TrimLeft(endpoint, "/"), referer, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	return body, err
}

// Apply posts a settings change to /apply_abstract.cgi: action = AES(cmd),
// each field value AES-encoded, plus httoken and submit_button = page.
func (c *Client) Apply(cmd, page string, fields map[string]string) (string, error) {
	_, sec, err := c.Page(page)
	if err != nil {
		return "", err
	}
	action, err := aesEncode(sec, cmd)
	if err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("action", action)
	form.Set("httoken", sec.Token)
	form.Set("submit_button", page)
	for k, v := range fields {
		enc, err := aesEncode(sec, v)
		if err != nil {
			return "", err
		}
		form.Set(k, enc)
	}
	_, body, err := c.do("POST", "/apply_abstract.cgi", page, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded")
	return body, err
}

// PageData loads a page and every cgi it declares, returning the merged
// editable Config and, per cgi, the read-only vars.
func (c *Client) PageData(page string) (*Config, map[string][]KV, error) {
	body, _, err := c.Page(page)
	if err != nil {
		return nil, nil, err
	}
	cfg := &Config{byName: map[string]int{}}
	reads := map[string][]KV{}
	for _, name := range pageCGIs(body) {
		js, err := c.GetCGI(name, page)
		if err != nil {
			reads[name] = []KV{{"(error)", err.Error()}}
			continue
		}
		cfg.merge(js)
		reads[name] = parseVars(js)
	}
	return cfg, reads, nil
}
