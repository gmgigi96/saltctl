// Command salt-fiber is a small CLI for the Salt Fiber Box (Arcadyan
// "Saltbox 1 A 1.0") web UI. It reproduces the browser's login and request
// signing so the box can be driven from scripts and schedulers.
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func main() {
	fs := flag.NewFlagSet("salt-fiber", flag.ContinueOnError)
	fs.Usage = usage
	url := fs.String("url", envOr("SALT_FIBER_URL", "http://192.168.1.1"), "base URL of the modem")
	user := fs.String("user", envOr("SALT_FIBER_USER", "admin"), "admin username")
	pass := fs.String("password", "", "admin password (else $SALT_FIBER_PASSWORD or -password-file)")
	passFile := fs.String("password-file", "", "file containing the admin password")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	args := fs.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	password, err := resolvePassword(*pass, *passFile)
	if err != nil {
		fatal(err)
	}
	newClient := func() *Client {
		c, err := NewClient(*url, *user, password)
		if err != nil {
			fatal(err)
		}
		if err := c.Login(); err != nil {
			fatal(err)
		}
		return c
	}

	switch args[0] {
	case "login":
		newClient()
		fmt.Println("login OK")
	case "led":
		cmdLED(newClient, args[1:])
	case "status":
		cmdDump(newClient(), "router_status.htm")
	case "devices":
		cmdDump(newClient(), "clients.htm")
	case "reboot":
		cmdReboot(newClient, args[1:])
	case "raw":
		cmdRaw(newClient, args[1:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage()
		os.Exit(2)
	}
}

// ---- led ----

var ledBrightness = map[int]string{0: "Off", 1: "Dim", 2: "Normal"}

func cmdLED(newClient func() *Client, args []string) {
	fsL := flag.NewFlagSet("led", flag.ExitOnError)
	target := fsL.String("target", "ALL", `which node: "ALL" or a MAC`)
	fsL.Parse(args)
	sub := "get"
	if fsL.NArg() > 0 {
		sub = fsL.Arg(0)
	}
	var level int
	switch sub {
	case "get":
		printLEDStatus(newClient())
		return
	case "off":
		level = 0
	case "dim":
		level = 1
	case "on":
		level = 2
	case "set":
		if fsL.NArg() < 2 {
			fatal(fmt.Errorf("usage: led set <0|1|2>"))
		}
		n, err := strconv.Atoi(fsL.Arg(1))
		if err != nil || n < 0 || n > 2 {
			fatal(fmt.Errorf("brightness must be 0 (Off), 1 (Dim) or 2 (Normal)"))
		}
		level = n
	default:
		fatal(fmt.Errorf("unknown led subcommand %q (get|on|off|dim|set)", sub))
	}

	c := newClient()
	body, err := c.PostSimple("/owl_cmd.cgi", "led_ctrl.htm", map[string]string{
		"cmd":           "setLED",
		"target":        *target,
		"led_brightness": strconv.Itoa(level),
	})
	if err != nil {
		fatal(err)
	}
	if !strings.Contains(body, `"OK"`) {
		fatal(fmt.Errorf("unexpected response: %s", strings.TrimSpace(body)))
	}
	fmt.Printf("LED -> %s (target %s)\n", ledBrightness[level], *target)
}

var ledStatusRe = regexp.MustCompile(`led_status\s*=\s*"([^"]*)"`)

func printLEDStatus(c *Client) {
	js, err := c.GetCGI("led", "led_ctrl.htm")
	if err != nil {
		fatal(err)
	}
	m := ledStatusRe.FindStringSubmatch(js)
	if m == nil {
		fatal(fmt.Errorf("led_status not found in response"))
	}
	fmt.Printf("%-20s %-18s %s\n", "NODE", "MAC", "BRIGHTNESS")
	for _, entry := range strings.Split(m[1], ",") {
		p := strings.Split(entry, "@")
		if len(p) < 3 {
			continue
		}
		b, _ := strconv.Atoi(p[2])
		name := ledBrightness[b]
		if name == "" {
			name = p[2]
		}
		fmt.Printf("%-20s %-18s %s\n", p[1], p[0], name)
	}
}

// ---- reboot ----

func cmdReboot(newClient func() *Client, args []string) {
	fsR := flag.NewFlagSet("reboot", flag.ExitOnError)
	yes := fsR.Bool("yes", false, "confirm; reboot drops internet/phone for ~60s")
	fsR.Parse(args)
	if !*yes {
		fatal(fmt.Errorf("refusing without -yes (reboot drops internet & phone for ~60s)"))
	}
	body, err := newClient().Apply("reboot", "system_r.htm", nil)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("reboot requested; box restarting. response: %s\n", strings.TrimSpace(firstLine(body)))
}

// ---- raw (escape hatch for any feature) ----

func cmdRaw(newClient func() *Client, args []string) {
	if len(args) == 0 {
		fatal(fmt.Errorf("usage: raw <page|get|post|apply> ..."))
	}
	switch args[0] {
	case "page":
		if len(args) < 2 {
			fatal(fmt.Errorf("usage: raw page <page.htm>"))
		}
		cmdDump(newClient(), args[1])
	case "get":
		if len(args) < 2 {
			fatal(fmt.Errorf("usage: raw get <cgiName> [-page referer.htm]"))
		}
		fsg := flag.NewFlagSet("raw get", flag.ExitOnError)
		page := fsg.String("page", args[1]+".htm", "referer page for the fresh token")
		fsg.Parse(args[2:])
		js, err := newClient().GetCGI(args[1], *page)
		if err != nil {
			fatal(err)
		}
		fmt.Print(js)
	case "post":
		if len(args) < 2 {
			fatal(fmt.Errorf(`usage: raw post <endpoint> [-page p.htm] k=v ...`))
		}
		fsp := flag.NewFlagSet("raw post", flag.ExitOnError)
		page := fsp.String("page", "index.htm", "referer page for the fresh token")
		fsp.Parse(args[2:])
		body, err := newClient().PostSimple(args[1], *page, kvArgs(fsp.Args()))
		if err != nil {
			fatal(err)
		}
		fmt.Print(body)
	case "apply":
		if len(args) < 2 {
			fatal(fmt.Errorf(`usage: raw apply <cmd> [-page p.htm] field=value ...`))
		}
		fsa := flag.NewFlagSet("raw apply", flag.ExitOnError)
		page := fsa.String("page", "index.htm", "submit_button / referer page")
		fsa.Parse(args[2:])
		body, err := newClient().Apply(args[1], *page, kvArgs(fsa.Args()))
		if err != nil {
			fatal(err)
		}
		fmt.Print(body)
	default:
		fatal(fmt.Errorf("unknown raw subcommand %q", args[0]))
	}
}

// cmdDump loads a page, fetches every cgi data file it declares, and prints the
// parsed variables. This exposes read data for any page in the UI.
func cmdDump(c *Client, page string) {
	body, _, err := c.page(page)
	if err != nil {
		fatal(err)
	}
	cgis := pageCGIs(body)
	if len(cgis) == 0 {
		fmt.Fprintf(os.Stderr, "(%s declares no cgi data files)\n", page)
		return
	}
	for _, name := range cgis {
		js, err := c.GetCGI(name, page)
		if err != nil {
			fmt.Printf("# cgi_%s.js: %v\n", name, err)
			continue
		}
		vars := parseVars(js)
		sort.SliceStable(vars, func(i, j int) bool { return vars[i].Key < vars[j].Key })
		fmt.Printf("# ---- cgi_%s.js ----\n", name)
		for _, v := range vars {
			fmt.Printf("%-28s = %s\n", v.Key, v.Val)
		}
	}
}

// ---- helpers ----

func resolvePassword(pass, passFile string) (string, error) {
	if pass != "" {
		return pass, nil
	}
	if passFile != "" {
		b, err := os.ReadFile(passFile)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	if v := os.Getenv("SALT_FIBER_PASSWORD"); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("no password: pass -password, -password-file, or set $SALT_FIBER_PASSWORD")
}

func kvArgs(args []string) map[string]string {
	m := map[string]string{}
	for _, a := range args {
		if i := strings.IndexByte(a, '='); i >= 0 {
			m[a[:i]] = a[i+1:]
		}
	}
	return m
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func usage() {
	fmt.Fprint(os.Stderr, `salt-fiber - CLI for the Salt Fiber Box (Arcadyan) web UI

Usage:
  salt-fiber [global flags] <command> [args]

Global flags:
  -url            base URL (default http://192.168.1.1, or $SALT_FIBER_URL)
  -user           admin username (default admin, or $SALT_FIBER_USER)
  -password       admin password (or $SALT_FIBER_PASSWORD, or -password-file)
  -password-file  file containing the admin password

Commands:
  login                      verify credentials
  led get                    show current LED brightness of each node
  led off|dim|on             set LEDs to Off / Dim / Normal
  led set <0|1|2> [-target]  set a specific level (-target ALL or a MAC)
  status                     dump overview / modem status
  devices                    dump connected-device data
  reboot -yes                reboot the box (drops service ~60s)
  raw page <page.htm>        dump all data a page exposes
  raw get <cgi> [-page p]    fetch one cgi data file
  raw post <endpoint> k=v... plaintext form POST (+ httoken)
  raw apply <cmd> k=v...     AES-signed settings POST to apply_abstract.cgi

Examples:
  export SALT_FIBER_PASSWORD=...
  salt-fiber led off
  salt-fiber led on
  salt-fiber status
`)
}
