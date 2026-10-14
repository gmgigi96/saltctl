// Command saltctl is a CLI for Salt's internet boxes. It selects a box model
// (only "fiberbox", the Salt Fiber Box, is supported today) and drives it
// through the saltbox capability interfaces, so other Salt models can be added
// later without changing the CLI.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/gmgigi96/saltctl/internal/saltbox"
	_ "github.com/gmgigi96/saltctl/internal/saltbox/fiberbox" // register the model
)

func main() {
	fs := flag.NewFlagSet("saltctl", flag.ContinueOnError)
	fs.Usage = usage
	url := fs.String("url", envOr("SALTCTL_URL", "http://192.168.1.1"), "base URL of the box")
	user := fs.String("user", envOr("SALTCTL_USER", "admin"), "admin username")
	model := fs.String("model", envOr("SALTCTL_MODEL", "fiberbox"), "box model")
	pass := fs.String("password", "", "admin password (else $SALTCTL_PASSWORD / -password-file)")
	passFile := fs.String("password-file", "", "file holding the admin password")
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
	dev := func() saltbox.Device {
		d, err := saltbox.Open(*model, saltbox.Options{URL: *url, User: *user, Pass: password})
		if err != nil {
			fatal(err)
		}
		if err := d.Login(); err != nil {
			fatal(err)
		}
		return d
	}

	switch args[0] {
	case "models":
		for _, m := range saltbox.Models() {
			fmt.Println(m)
		}
	case "login":
		d := dev()
		fmt.Printf("login OK (%s)\n", d.Model())
	case "areas":
		cmdAreas(dev())
	case "get":
		cmdGet(dev(), args[1:])
	case "set":
		cmdSet(dev(), args[1:])
	case "status":
		cmdGet(dev(), []string{"modem"})
	case "devices":
		cmdGet(dev(), []string{"devices"})
	case "led":
		cmdLED(dev(), args[1:])
	case "wifi":
		cmdWiFi(dev(), args[1:])
	case "guest":
		cmdGuest(dev(), args[1:])
	case "dect":
		cmdDECT(dev(), args[1:])
	case "ddns":
		cmdDDNS(dev(), args[1:])
	case "dmz":
		cmdDMZ(dev(), args[1:])
	case "upnp":
		cmdToggle(dev(), "upnp", args[1:])
	case "mesh":
		cmdMesh(dev(), args[1:])
	case "reboot":
		cmdReboot(dev(), args[1:])
	case "config":
		cmdConfig(dev(), args[1:])
	case "portforward", "pf":
		cmdPortForward(dev(), args[1:])
	case "dhcp":
		cmdDHCP(dev(), args[1:])
	case "raw":
		cmdRaw(dev(), args[1:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage()
		os.Exit(2)
	}
}

// capOf returns the capability T of dev, or exits if unsupported.
func capOf[T any](dev saltbox.Device) T {
	v, ok := dev.(T)
	if !ok {
		fatal(fmt.Errorf("%s does not support this feature", dev.Model()))
	}
	return v
}

// ---- settings: areas / get / set ----

func cmdAreas(dev saltbox.Device) {
	s := capOf[saltbox.Settings](dev)
	fmt.Printf("%-18s %-4s %s\n", "AREA", "RW", "DESCRIPTION")
	for _, a := range s.Areas() {
		rw := "r"
		if a.Writable {
			rw = "rw"
		}
		risk := ""
		if a.Risky {
			risk = "  [risky]"
		}
		fmt.Printf("%-18s %-4s %s%s\n", a.Name, rw, a.Desc, risk)
	}
}

func cmdGet(dev saltbox.Device, args []string) {
	if len(args) == 0 {
		fatal(fmt.Errorf("usage: get <area>  (see `areas`)"))
	}
	s := capOf[saltbox.Settings](dev)
	fields, reads, err := s.GetArea(args[0])
	if err != nil {
		fatal(err)
	}
	if len(fields) > 0 {
		fmt.Printf("# %s — editable (name = value)\n", args[0])
		for _, f := range fields {
			fmt.Printf("%-30s = %s\n", f.Name, f.Value)
		}
	}
	cgis := make([]string, 0, len(reads))
	for k := range reads {
		cgis = append(cgis, k)
	}
	sort.Strings(cgis)
	for _, cgi := range cgis {
		if len(reads[cgi]) == 0 {
			continue
		}
		fmt.Printf("# %s — read-only (cgi_%s.js)\n", args[0], cgi)
		for _, v := range reads[cgi] {
			fmt.Printf("%-30s = %s\n", v.Name, v.Value)
		}
	}
}

func cmdSet(dev saltbox.Device, args []string) {
	fss := flag.NewFlagSet("set", flag.ExitOnError)
	yes := fss.Bool("yes", false, "confirm a risky area change")
	fss.Parse(args)
	rest := fss.Args()
	if len(rest) < 2 {
		fatal(fmt.Errorf("usage: set <area> name=value ...  (see `areas`)"))
	}
	changes := kvArgs(rest[1:])
	if len(changes) == 0 {
		fatal(fmt.Errorf("no name=value pairs given"))
	}
	for k, v := range changes {
		fmt.Printf("  %s -> %q\n", k, v)
	}
	body, err := capOf[saltbox.Settings](dev).SetArea(rest[0], changes, *yes)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("applied. %s\n", respLine(body))
}

// ---- led ----

var ledNames = map[int]string{0: "Off", 1: "Dim", 2: "Normal"}

func cmdLED(dev saltbox.Device, args []string) {
	l := capOf[saltbox.LED](dev)
	fsL := flag.NewFlagSet("led", flag.ExitOnError)
	target := fsL.String("target", "ALL", `"ALL" or a MAC`)
	fsL.Parse(args)
	sub := "get"
	if fsL.NArg() > 0 {
		sub = fsL.Arg(0)
	}
	level := -1
	switch sub {
	case "get":
		nodes, err := l.LEDStatus()
		if err != nil {
			fatal(err)
		}
		fmt.Printf("%-18s %-18s %s\n", "NODE", "MAC", "BRIGHTNESS")
		for _, n := range nodes {
			name := ledNames[n.Level]
			if name == "" {
				name = strconv.Itoa(n.Level)
			}
			fmt.Printf("%-18s %-18s %s\n", n.Name, n.MAC, name)
		}
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
			fatal(fmt.Errorf("level must be 0 (Off), 1 (Dim) or 2 (Normal)"))
		}
		level = n
	default:
		fatal(fmt.Errorf("usage: led get|on|off|dim|set <0|1|2>"))
	}
	if err := l.SetLED(*target, level); err != nil {
		fatal(err)
	}
	fmt.Printf("LED -> %s (target %s)\n", ledNames[level], *target)
}

// ---- wifi ----

func cmdWiFi(dev saltbox.Device, args []string) {
	fsw := flag.NewFlagSet("wifi", flag.ExitOnError)
	band := fsw.String("band", "both", "24, 5, or both")
	yes := fsw.Bool("yes", false, "confirm enabling/disabling radios")
	fsw.Parse(args)
	sub := "get"
	if fsw.NArg() > 0 {
		sub = fsw.Arg(0)
	}
	if sub == "get" {
		cmdGet(dev, []string{"wifi"})
		return
	}
	w := capOf[saltbox.WiFi](dev)
	bands := bandList(*band)
	switch sub {
	case "on", "off":
		if !*yes {
			fatal(fmt.Errorf("`wifi %s` changes the radios; re-run with -yes", sub))
		}
		for _, bnd := range bands {
			must(w.SetRadio(bnd, sub == "on"))
			fmt.Printf("radio %s -> %s\n", bnd, sub)
		}
	case "ssid":
		v := fsw.Arg(1)
		if v == "" {
			fatal(fmt.Errorf("usage: wifi ssid <name> [-band 24|5|both]"))
		}
		for _, bnd := range bands {
			must(w.SetSSID(bnd, v))
			fmt.Printf("ssid %s -> %q\n", bnd, v)
		}
	case "password":
		v := fsw.Arg(1)
		if len(v) < 8 {
			fatal(fmt.Errorf("Wi-Fi password must be at least 8 characters"))
		}
		for _, bnd := range bands {
			must(w.SetWiFiPassword(bnd, v))
			fmt.Printf("password %s updated\n", bnd)
		}
	case "channel":
		n, err := strconv.Atoi(fsw.Arg(1))
		if err != nil {
			fatal(fmt.Errorf("usage: wifi channel <n|0=auto> [-band 24|5]"))
		}
		for _, bnd := range bands {
			must(w.SetChannel(bnd, n))
			fmt.Printf("channel %s -> %d\n", bnd, n)
		}
	default:
		fatal(fmt.Errorf("usage: wifi get|on|off|ssid|password|channel"))
	}
}

// ---- guest ----

func cmdGuest(dev saltbox.Device, args []string) {
	fsg := flag.NewFlagSet("guest", flag.ExitOnError)
	band := fsg.String("band", "both", "24, 5, or both")
	fsg.Parse(args)
	if fsg.NArg() == 0 {
		cmdGet(dev, []string{"guest"})
		return
	}
	g := capOf[saltbox.Guest](dev)
	switch fsg.Arg(0) {
	case "on", "off":
		must(g.SetGuest(fsg.Arg(0) == "on"))
		fmt.Printf("guest Wi-Fi -> %s\n", fsg.Arg(0))
	case "ssid":
		for _, bnd := range bandList(*band) {
			must(g.SetGuestSSID(bnd, fsg.Arg(1)))
			fmt.Printf("guest ssid %s -> %q\n", bnd, fsg.Arg(1))
		}
	case "password":
		if len(fsg.Arg(1)) < 8 {
			fatal(fmt.Errorf("password must be at least 8 characters"))
		}
		for _, bnd := range bandList(*band) {
			must(g.SetGuestPassword(bnd, fsg.Arg(1)))
			fmt.Printf("guest password %s updated\n", bnd)
		}
	default:
		fatal(fmt.Errorf("usage: guest on|off|ssid <name>|password <pw>"))
	}
}

// ---- dect / ddns / dmz / upnp ----

func cmdDECT(dev saltbox.Device, args []string) {
	d := capOf[saltbox.DECT](dev)
	if len(args) == 0 {
		cmdGet(dev, []string{"dect"})
		return
	}
	switch args[0] {
	case "on":
		must(d.SetDECT(true))
		fmt.Println("DECT -> on")
	case "off":
		must(d.SetDECT(false))
		fmt.Println("DECT -> off")
	case "eco":
		on := len(args) > 1 && args[1] == "on"
		must(d.SetDECTEco(on))
		fmt.Printf("DECT eco -> %v\n", on)
	default:
		fatal(fmt.Errorf("usage: dect on|off|eco on|off"))
	}
}

func cmdDDNS(dev saltbox.Device, args []string) {
	fsd := flag.NewFlagSet("ddns", flag.ExitOnError)
	svc := fsd.String("service", "", "provider, e.g. dyndns")
	host := fsd.String("host", "", "hostname")
	user := fsd.String("user", "", "username")
	pw := fsd.String("pass", "", "password")
	fsd.Parse(args)
	d := capOf[saltbox.DDNS](dev)
	switch fsd.Arg(0) {
	case "", "get":
		cmdGet(dev, []string{"ddns"})
	case "off":
		must(d.SetDDNS(false, "", "", "", ""))
		fmt.Println("DDNS -> off")
	case "set":
		must(d.SetDDNS(true, *svc, *host, *user, *pw))
		fmt.Println("DDNS -> on")
	default:
		fatal(fmt.Errorf("usage: ddns get|off|set -service X -host H [-user U -pass P]"))
	}
}

func cmdDMZ(dev saltbox.Device, args []string) {
	fsz := flag.NewFlagSet("dmz", flag.ExitOnError)
	yes := fsz.Bool("yes", false, "confirm (exposes a host to the internet)")
	fsz.Parse(args)
	d := capOf[saltbox.DMZ](dev)
	switch fsz.Arg(0) {
	case "", "get":
		cmdGet(dev, []string{"firewall-dmz"})
	case "off":
		must(d.SetDMZ(""))
		fmt.Println("DMZ -> off")
	case "set":
		if !*yes {
			fatal(fmt.Errorf("DMZ exposes a host to the internet; re-run with -yes"))
		}
		ip := fsz.Arg(1)
		if ip == "" {
			fatal(fmt.Errorf("usage: dmz set <lan-ip> -yes"))
		}
		must(d.SetDMZ(ip))
		fmt.Printf("DMZ -> %s\n", ip)
	default:
		fatal(fmt.Errorf("usage: dmz get|off|set <ip> -yes"))
	}
}

func cmdToggle(dev saltbox.Device, which string, args []string) {
	// only upnp today
	u := capOf[saltbox.UPnP](dev)
	if len(args) == 0 || args[0] == "get" {
		cmdGet(dev, []string{which})
		return
	}
	switch args[0] {
	case "on":
		must(u.SetUPnP(true))
		fmt.Println("UPnP -> on")
	case "off":
		must(u.SetUPnP(false))
		fmt.Println("UPnP -> off")
	default:
		fatal(fmt.Errorf("usage: %s on|off|get", which))
	}
}

// ---- mesh / reboot ----

func cmdMesh(dev saltbox.Device, args []string) {
	if len(args) == 0 {
		fatal(fmt.Errorf("usage: mesh optimize|reboot|reset [-yes]"))
	}
	fsm := flag.NewFlagSet("mesh", flag.ExitOnError)
	yes := fsm.Bool("yes", false, "confirm reboot/reset")
	fsm.Parse(args[1:])
	if (args[0] == "reboot" || args[0] == "reset") && !*yes {
		fatal(fmt.Errorf("`mesh %s` is disruptive; re-run with -yes", args[0]))
	}
	body, err := capOf[saltbox.Mesh](dev).MeshAction(args[0])
	if err != nil {
		fatal(err)
	}
	fmt.Printf("mesh %s -> %s\n", args[0], respLine(body))
}

func cmdReboot(dev saltbox.Device, args []string) {
	fsr := flag.NewFlagSet("reboot", flag.ExitOnError)
	yes := fsr.Bool("yes", false, "confirm (drops internet & phone ~60s)")
	fsr.Parse(args)
	if !*yes {
		fatal(fmt.Errorf("reboot drops internet & phone ~60s; re-run with -yes"))
	}
	body, err := capOf[saltbox.Rebooter](dev).Reboot()
	if err != nil {
		fatal(err)
	}
	fmt.Printf("reboot requested. %s\n", respLine(body))
}

// ---- config save / restore ----

func cmdConfig(dev saltbox.Device, args []string) {
	bk := capOf[saltbox.Backup](dev)
	if len(args) == 0 {
		fatal(fmt.Errorf("usage: config save [-o file] | config restore <file> -yes"))
	}
	switch args[0] {
	case "save":
		fc := flag.NewFlagSet("config save", flag.ExitOnError)
		out := fc.String("o", "", "output file (default: the box's own name)")
		fc.Parse(args[1:])
		rc, fn, err := bk.ConfigSave()
		if err != nil {
			fatal(err)
		}
		defer rc.Close()
		path := *out
		if path == "" {
			path = fn
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			fatal(err)
		}
		n, err := io.Copy(f, rc)
		f.Close()
		if err != nil {
			fatal(err)
		}
		fmt.Printf("saved %d bytes to %s (encrypted config blob)\n", n, path)
	case "restore":
		fc := flag.NewFlagSet("config restore", flag.ExitOnError)
		yes := fc.Bool("yes", false, "confirm (overwrites config and reboots)")
		fc.Parse(args[1:])
		file := fc.Arg(0)
		if file == "" {
			fatal(fmt.Errorf("usage: config restore <file> -yes"))
		}
		if !*yes {
			fatal(fmt.Errorf("restore overwrites all settings and reboots the box; re-run with -yes"))
		}
		f, err := os.Open(file)
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		body, err := bk.ConfigRestore(f, filepath.Base(file))
		if err != nil {
			fatal(err)
		}
		fmt.Printf("restore uploaded; box will apply & reboot. %s\n", respLine(body))
	default:
		fatal(fmt.Errorf("usage: config save [-o file] | config restore <file> -yes"))
	}
}

// ---- port forwarding ----

func cmdPortForward(dev saltbox.Device, args []string) {
	pf := capOf[saltbox.PortForward](dev)
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list":
		rules, err := pf.PortForwardList()
		if err != nil {
			fatal(err)
		}
		if len(rules) == 0 {
			fmt.Println("(no port-forwarding rules)")
			return
		}
		fmt.Printf("%-3s %-3s %-5s %-17s %-13s %s\n", "IDX", "EN", "PROTO", "INTERNAL IP", "PUBLIC", "PRIVATE")
		for _, r := range rules {
			en := "off"
			if r.Enabled {
				en = "on"
			}
			fmt.Printf("%-3d %-3s %-5s %-17s %-13s %s\n", r.Index, en, r.Protocol, r.InternalIP,
				portRange(r.PublicStart, r.PublicEnd), portRange(r.PrivateStart, r.PrivateEnd))
		}
	case "add":
		f := flag.NewFlagSet("pf add", flag.ExitOnError)
		proto := f.String("proto", "tcp", "tcp|udp|both")
		ip := f.String("ip", "", "internal (LAN) IP")
		public := f.String("public", "", "public port or range a-b")
		private := f.String("private", "", "internal port or range (default = public)")
		yes := f.Bool("yes", false, "confirm (opens a port from the internet)")
		f.Parse(args[1:])
		if !*yes {
			fatal(fmt.Errorf("port forwarding exposes a service to the internet; re-run with -yes"))
		}
		ps, pe := parsePorts(*public)
		rs, re := parsePorts(*private)
		idx, err := pf.PortForwardAdd(saltbox.PortForwardRule{
			Protocol: *proto, InternalIP: *ip,
			PublicStart: ps, PublicEnd: pe, PrivateStart: rs, PrivateEnd: re,
		})
		if err != nil {
			fatal(err)
		}
		fmt.Printf("added port-forward rule at slot %d\n", idx)
	case "rm":
		f := flag.NewFlagSet("pf rm", flag.ExitOnError)
		yes := f.Bool("yes", false, "confirm")
		f.Parse(args[1:])
		if !*yes {
			fatal(fmt.Errorf("re-run with -yes to remove the rule"))
		}
		n, err := strconv.Atoi(f.Arg(0))
		if err != nil {
			fatal(fmt.Errorf("usage: portforward rm <index> -yes"))
		}
		must(pf.PortForwardRemove(n))
		fmt.Printf("removed port-forward rule %d\n", n)
	default:
		fatal(fmt.Errorf("usage: portforward list|add|rm"))
	}
}

// ---- dhcp reservations ----

func cmdDHCP(dev saltbox.Device, args []string) {
	d := capOf[saltbox.DHCP](dev)
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list":
		res, err := d.DHCPList()
		if err != nil {
			fatal(err)
		}
		if len(res) == 0 {
			fmt.Println("(no DHCP reservations)")
			return
		}
		fmt.Printf("%-3s %-4s %-18s %-16s %s\n", "IDX", "ACT", "MAC", "IP", "NAME")
		for _, r := range res {
			act := "off"
			if r.Active {
				act = "on"
			}
			fmt.Printf("%-3d %-4s %-18s %-16s %s\n", r.Index, act, r.MAC, r.IP, r.Name)
		}
	case "reserve":
		f := flag.NewFlagSet("dhcp reserve", flag.ExitOnError)
		mac := f.String("mac", "", "device MAC")
		ip := f.String("ip", "", "IP to assign")
		name := f.String("name", "", "label (optional)")
		f.Parse(args[1:])
		idx, err := d.DHCPReserve(*mac, *ip, *name)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("reserved %s -> %s at slot %d\n", *mac, *ip, idx)
	case "rm":
		n, err := strconv.Atoi(firstArg(args[1:]))
		if err != nil {
			fatal(fmt.Errorf("usage: dhcp rm <index>"))
		}
		must(d.DHCPRemove(n))
		fmt.Printf("removed reservation %d\n", n)
	default:
		fatal(fmt.Errorf("usage: dhcp list|reserve|rm"))
	}
}

// parsePorts parses "80" or "80-90" into (start, end). end=0 when single/empty.
func parsePorts(s string) (int, int) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		a, _ := strconv.Atoi(strings.TrimSpace(s[:i]))
		b, _ := strconv.Atoi(strings.TrimSpace(s[i+1:]))
		return a, b
	}
	n, _ := strconv.Atoi(s)
	return n, 0
}

func portRange(a, b int) string {
	if b > 0 && b != a {
		return fmt.Sprintf("%d-%d", a, b)
	}
	return strconv.Itoa(a)
}

func firstArg(a []string) string {
	if len(a) > 0 {
		return a[0]
	}
	return ""
}

// ---- raw ----

func cmdRaw(dev saltbox.Device, args []string) {
	r := capOf[saltbox.Raw](dev)
	if len(args) == 0 {
		fatal(fmt.Errorf("usage: raw get|post|apply ..."))
	}
	switch args[0] {
	case "get":
		if len(args) < 2 {
			fatal(fmt.Errorf("usage: raw get <cgi> [-page p.htm]"))
		}
		f := flag.NewFlagSet("raw get", flag.ExitOnError)
		page := f.String("page", args[1]+".htm", "referer page")
		f.Parse(args[2:])
		out, err := r.RawGet(args[1], *page)
		if err != nil {
			fatal(err)
		}
		fmt.Print(out)
	case "post":
		if len(args) < 2 {
			fatal(fmt.Errorf("usage: raw post <endpoint> [-page p] k=v ..."))
		}
		f := flag.NewFlagSet("raw post", flag.ExitOnError)
		page := f.String("page", "index.htm", "referer page")
		f.Parse(args[2:])
		out, err := r.RawPost(args[1], *page, kvArgs(f.Args()))
		if err != nil {
			fatal(err)
		}
		fmt.Print(out)
	case "apply":
		if len(args) < 2 {
			fatal(fmt.Errorf("usage: raw apply <cmd> [-page p] field=value ..."))
		}
		f := flag.NewFlagSet("raw apply", flag.ExitOnError)
		page := f.String("page", "index.htm", "submit_button / referer page")
		f.Parse(args[2:])
		out, err := r.RawApply(args[1], *page, kvArgs(f.Args()))
		if err != nil {
			fatal(err)
		}
		fmt.Print(out)
	default:
		fatal(fmt.Errorf("unknown raw subcommand %q", args[0]))
	}
}

// ---- helpers ----

func bandList(band string) []string {
	switch band {
	case "both", "":
		return []string{"24", "5"}
	default:
		return []string{band}
	}
}

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

func respLine(body string) string {
	s := strings.TrimSpace(body)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "(empty response)"
	}
	return "response: " + s
}

func must(err error) {
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func usage() {
	fmt.Fprint(os.Stderr, `saltctl - CLI for Salt internet boxes (model: fiberbox = Salt Fiber Box)

Usage:
  saltctl [global flags] <command> [args]

Global flags:
  -url / $SALTCTL_URL            base URL (default http://192.168.1.1)
  -user / $SALTCTL_USER          admin username (default admin)
  -model / $SALTCTL_MODEL        box model (default fiberbox)
  -password / -password-file     admin password (or $SALTCTL_PASSWORD)

Commands:
  models                         list supported box models
  login                          verify credentials
  status | devices               model/firmware | connected devices
  areas                          list feature areas
  get <area>                     read an area's settings
  set <area> k=v ... [-yes]      change settings (risky areas need -yes)
  led get|off|dim|on|set <0-2>   LED brightness
  wifi get|on|off|ssid|password|channel [-band 24|5|both] [-yes]
  guest on|off|ssid <n>|password <pw> [-band]
  dect on|off|eco on|off
  ddns get|off|set -service S -host H [-user U -pass P]
  dmz get|off|set <ip> -yes
  upnp get|on|off
  mesh optimize|reboot|reset [-yes]
  reboot -yes
  config save [-o file]          download the (encrypted) config
  config restore <file> -yes     upload a saved config (reboots)
  portforward list|add|rm        port-forwarding rules (add/rm need -yes)
  dhcp list|reserve|rm           DHCP static reservations
  raw get|post|apply ...         escape hatch for anything else

Example:
  export SALTCTL_PASSWORD=...
  saltctl led off
  saltctl wifi ssid MyHome -band both
  saltctl set ddns ddns_enable=1    # or:  saltctl ddns set -host home.example
`)
}
