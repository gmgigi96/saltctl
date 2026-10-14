// Command saltctl is a small CLI for Salt's internet boxes. It reproduces the
// browser's login and request signing so a box can be driven from scripts and
// schedulers. Today it targets the Salt Fiber Box (Arcadyan "Saltbox 1 A
// 1.0"); the other Salt fibre boxes (X6 / X6 Pro / Wi-Fi 7+) share the same
// Arcadyan web UI, while the 5G GigaBox would need its own backend.
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
	fs := flag.NewFlagSet("saltctl", flag.ContinueOnError)
	fs.Usage = usage
	url := fs.String("url", envOr("SALTCTL_URL", "http://192.168.1.1"), "base URL of the modem")
	user := fs.String("user", envOr("SALTCTL_USER", "admin"), "admin username")
	pass := fs.String("password", "", "admin password (else $SALTCTL_PASSWORD or -password-file)")
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
	case "areas":
		cmdAreas()
	case "get":
		cmdGet(newClient, args[1:])
	case "set":
		cmdSet(newClient, args[1:])
	case "wifi":
		cmdWifi(newClient, args[1:])
	case "mesh":
		cmdMesh(newClient, args[1:])
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

// ---- areas / get / set (generic settings layer) ----

func cmdAreas() {
	fmt.Printf("%-18s %-4s %s\n", "AREA", "RW", "DESCRIPTION")
	for _, a := range areas {
		rw := "r"
		if a.Cmd != "" {
			rw = "rw"
		}
		risk := ""
		if a.Risky {
			risk = "  [risky: can drop connectivity]"
		}
		fmt.Printf("%-18s %-4s %s%s\n", a.Name, rw, a.Desc, risk)
	}
	fmt.Println("\nget <area>            read an area's settings")
	fmt.Println("set <area> k=v ...    change settings (writable areas; -yes for risky)")
}

func cmdGet(newClient func() *Client, args []string) {
	if len(args) == 0 {
		fatal(fmt.Errorf("usage: get <area>   (see `areas`)"))
	}
	a, ok := findArea(args[0])
	if !ok {
		fatal(fmt.Errorf("unknown area %q (see `areas`)", args[0]))
	}
	cfg, reads, err := newClient().PageData(a.Page)
	if err != nil {
		fatal(err)
	}
	if len(cfg.Fields) > 0 {
		fmt.Printf("# %s — editable settings (name = value)\n", a.Name)
		for _, f := range cfg.Fields {
			fmt.Printf("%-30s = %s\n", f.Name, f.Value)
		}
	}
	for cgi, vars := range reads {
		if len(vars) == 0 {
			continue
		}
		fmt.Printf("# %s — read-only (cgi_%s.js)\n", a.Name, cgi)
		for _, v := range vars {
			fmt.Printf("%-30s = %s\n", v.Key, v.Val)
		}
	}
}

func cmdSet(newClient func() *Client, args []string) {
	fss := flag.NewFlagSet("set", flag.ExitOnError)
	yes := fss.Bool("yes", false, "confirm a change to a risky area")
	fss.Parse(args)
	rest := fss.Args()
	if len(rest) < 2 {
		fatal(fmt.Errorf("usage: set <area> name=value ...   (see `areas`)"))
	}
	a, ok := findArea(rest[0])
	if !ok {
		fatal(fmt.Errorf("unknown area %q (see `areas`)", rest[0]))
	}
	if a.Cmd == "" {
		fatal(fmt.Errorf("area %q is read-only here; use `raw apply` for its actions", a.Name))
	}
	if a.Risky && !*yes {
		fatal(fmt.Errorf("area %q can drop connectivity; re-run with -yes", a.Name))
	}
	pairs := kvArgs(rest[1:])
	if len(pairs) == 0 {
		fatal(fmt.Errorf("no name=value pairs given"))
	}
	c := newClient()
	cfg, _, err := c.PageData(a.Page)
	if err != nil {
		fatal(err)
	}
	changes := map[string]string{}
	for name, val := range pairs {
		f, ok := cfg.lookup(name)
		if !ok {
			fatal(fmt.Errorf("unknown setting %q for area %s (try `get %s`)", name, a.Name, a.Name))
		}
		changes[f.ID] = val
		fmt.Printf("  %s (%s): %q -> %q\n", name, f.ID, f.Value, val)
	}
	body, err := c.Apply(a.Cmd, a.Page, changes)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("applied (cmd=%s). response: %s\n", a.Cmd, strings.TrimSpace(firstLine(body)))
}

// ---- wifi / mesh convenience ----

func cmdWifi(newClient func() *Client, args []string) {
	fsw := flag.NewFlagSet("wifi", flag.ExitOnError)
	yes := fsw.Bool("yes", false, "confirm enabling/disabling Wi-Fi")
	fsw.Parse(args)
	sub := "get"
	if fsw.NArg() > 0 {
		sub = fsw.Arg(0)
	}
	switch sub {
	case "get":
		cmdGet(newClient, []string{"wifi"})
	case "on", "off":
		if !*yes {
			fatal(fmt.Errorf("`wifi %s` changes the radios; re-run with -yes", sub))
		}
		val := "1"
		if sub == "off" {
			val = "0"
		}
		c := newClient()
		for _, pair := range [][2]string{{"wifi24", "Wireless_enable24G"}, {"wifi5", "Wireless_enable5G"}} {
			a, _ := findArea(pair[0])
			cfg, _, err := c.PageData(a.Page)
			if err != nil {
				fatal(err)
			}
			f, ok := cfg.lookup(pair[1])
			if !ok {
				fatal(fmt.Errorf("%s: field %s not found", pair[0], pair[1]))
			}
			if _, err := c.Apply(a.Cmd, a.Page, map[string]string{f.ID: val}); err != nil {
				fatal(err)
			}
			fmt.Printf("%s %s -> %s\n", pair[0], pair[1], val)
		}
	default:
		fatal(fmt.Errorf("usage: wifi [get|on|off]"))
	}
}

func cmdMesh(newClient func() *Client, args []string) {
	if len(args) == 0 {
		fatal(fmt.Errorf("usage: mesh <optimize|reboot|reset> [-yes]"))
	}
	fsm := flag.NewFlagSet("mesh", flag.ExitOnError)
	yes := fsm.Bool("yes", false, "confirm reboot/reset")
	fsm.Parse(args[1:])
	var cmd string
	switch args[0] {
	case "optimize":
		cmd = "getdiscoptimising"
	case "reboot":
		cmd = "reboot"
	case "reset":
		cmd = "reset"
	default:
		fatal(fmt.Errorf("usage: mesh <optimize|reboot|reset>"))
	}
	if (cmd == "reboot" || cmd == "reset") && !*yes {
		fatal(fmt.Errorf("`mesh %s` is disruptive; re-run with -yes", args[0]))
	}
	body, err := newClient().PostSimple("/owl_cmd.cgi", "owl_discs.htm", map[string]string{"cmd": cmd})
	if err != nil {
		fatal(err)
	}
	fmt.Printf("mesh %s -> %s\n", args[0], strings.TrimSpace(firstLine(body)))
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
	if v := os.Getenv("SALTCTL_PASSWORD"); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("no password: pass -password, -password-file, or set $SALTCTL_PASSWORD")
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
	fmt.Fprint(os.Stderr, `saltctl - CLI for Salt internet boxes (currently the Salt Fiber Box / Arcadyan)

Usage:
  saltctl [global flags] <command> [args]

Global flags:
  -url            base URL (default http://192.168.1.1, or $SALTCTL_URL)
  -user           admin username (default admin, or $SALTCTL_USER)
  -password       admin password (or $SALTCTL_PASSWORD, or -password-file)
  -password-file  file containing the admin password

Commands:
  login                      verify credentials
  areas                      list feature areas (what get/set can target)
  get <area>                 read an area's settings + status
  set <area> k=v ... [-yes]  change settings (writable areas; -yes if risky)
  led get|off|dim|on         LED brightness (Off/Dim/Normal)
  led set <0|1|2> [-target]  set a specific LED level
  wifi get|on|off [-yes]     show / enable / disable the Wi-Fi radios
  mesh optimize|reboot|reset Smart Wi-Fi mesh actions (-yes for reboot/reset)
  status                     model / firmware / serial
  devices                    connected-device data
  reboot -yes                reboot the box (drops service ~60s)
  raw page <page.htm>        dump all data a page exposes
  raw get <cgi> [-page p]    fetch one cgi data file
  raw post <endpoint> k=v... plaintext form POST (+ httoken)
  raw apply <cmd> k=v...     AES-signed settings POST to apply_abstract.cgi

Examples:
  export SALTCTL_PASSWORD=...
  saltctl led off
  saltctl led on
  saltctl status
`)
}
