// Package fiberbox implements the Salt Fiber Box (Arcadyan "Saltbox 1 A 1.0").
// It registers itself as model "fiberbox" and is, for now, the only supported
// Salt box. Other fibre models (X6 / X6 Pro / Wi-Fi 7+) share this Arcadyan
// protocol and would mostly reuse it; the 5G GigaBox would need its own driver.
package fiberbox

import (
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gmgigi96/saltctl/internal/arcadyan"
	"github.com/gmgigi96/saltctl/internal/saltbox"
)

func init() {
	saltbox.Register("fiberbox", func(o saltbox.Options) (saltbox.Device, error) {
		c, err := arcadyan.New(o.URL, o.User, o.Pass)
		if err != nil {
			return nil, err
		}
		return &Box{c: c}, nil
	})
}

// Box is a Salt Fiber Box device.
type Box struct{ c *arcadyan.Client }

func (b *Box) Model() string { return "Salt Fiber Box (Arcadyan Saltbox 1 A 1.0)" }
func (b *Box) Login() error  { return b.c.Login() }

// --- settings areas ---

type areaDef struct {
	Name, Page, Cmd, Desc string
	Risky                 bool
}

var areaList = []areaDef{
	{"state", "state.htm", "", "Overview (PON, telephony, Wi-Fi)", false},
	{"modem", "router_status.htm", "", "Model / firmware / serial", false},
	{"pon", "pon_status.htm", "", "Fiber / GPON status", false},
	{"devices", "clients.htm", "", "Connected devices", false},
	{"lte", "lte_status.htm", "", "LTE status", false},
	{"mesh", "owl_status.htm", "", "Smart Wi-Fi mesh topology", false},
	{"extenders", "owl_discs.htm", "", "Mesh extenders", false},
	{"wifi", "wlan_combine1.htm", "ui_wireless_combine", "Combined Smart Wi-Fi", true},
	{"wifi24", "wlan_24g1.htm", "ui_wireless", "2.4 GHz radio", true},
	{"wifi5", "wlan_5g1.htm", "ui_wireless", "5 GHz radio", true},
	{"guest", "wlan_guest.htm", "ui_wireless_guest", "Guest Wi-Fi", false},
	{"wifi-macfilter", "wlan_mac_filter.htm", "ui_wireless", "Wi-Fi MAC filter", false},
	{"lan", "lan.htm", "ui_lan", "LAN IP + DHCP server", true},
	{"dhcp-reservations", "lan_dhcp_static.htm", "ui_dhcp_static", "DHCP static leases", false},
	{"nat", "nat.htm", "ui_rip", "NAT settings", false},
	{"portforward", "nat_port_map.htm", "ui_firewall", "Port forwarding", false},
	{"special-app", "nat_sp.htm", "ui_firewall", "NAT special applications", false},
	{"ddns", "dynamic_dns.htm", "ui_ddns", "Dynamic DNS", false},
	{"upnp", "upnp.htm", "ui_upnp", "UPnP", false},
	{"firewall", "firewall_main.htm", "ui_firewall", "Firewall switches", true},
	{"firewall-ac", "firewall_ac.htm", "ui_firewall", "Firewall access control", false},
	{"firewall-url", "firewall_url.htm", "ui_firewall", "URL blocking", false},
	{"firewall-dmz", "firewall_dmz.htm", "ui_firewall", "DMZ host", true},
	{"firewall-spi", "firewall_spi_h.htm", "ui_firewall", "SPI firewall", false},
	{"ipv6-firewall", "ipv6_firewall.htm", "ui_firewall6", "IPv6 firewall", true},
	{"dect", "tel_dect.htm", "", "DECT telephony", false},
	{"usb", "usb_basic.htm", "", "USB status", false},
	{"usb-media", "usb_media.htm", "ui_usb_mediaserver", "USB media (DLNA) server", false},
	{"power-saving", "power_saving.htm", "ui_ECOmode", "Eco / Wi-Fi & DECT scheduling", false},
}

func (b *Box) areaByName(name string) (areaDef, bool) {
	for _, a := range areaList {
		if a.Name == name {
			return a, true
		}
	}
	return areaDef{}, false
}

func (b *Box) Areas() []saltbox.Area {
	out := make([]saltbox.Area, 0, len(areaList))
	for _, a := range areaList {
		out = append(out, saltbox.Area{Name: a.Name, Desc: a.Desc, Writable: a.Cmd != "", Risky: a.Risky})
	}
	return out
}

func (b *Box) GetArea(name string) ([]saltbox.Setting, map[string][]saltbox.Setting, error) {
	a, ok := b.areaByName(name)
	if !ok {
		return nil, nil, fmt.Errorf("unknown area %q", name)
	}
	cfg, reads, err := b.c.PageData(a.Page)
	if err != nil {
		return nil, nil, err
	}
	fields := make([]saltbox.Setting, 0, len(cfg.Fields))
	for _, f := range cfg.Fields {
		fields = append(fields, saltbox.Setting{Name: f.Name, Value: f.Value})
	}
	rd := map[string][]saltbox.Setting{}
	for cgi, vars := range reads {
		for _, v := range vars {
			rd[cgi] = append(rd[cgi], saltbox.Setting{Name: v.Key, Value: v.Val})
		}
	}
	return fields, rd, nil
}

func (b *Box) SetArea(name string, changes map[string]string, confirmRisky bool) (string, error) {
	a, ok := b.areaByName(name)
	if !ok {
		return "", fmt.Errorf("unknown area %q", name)
	}
	if a.Cmd == "" {
		return "", fmt.Errorf("area %q is read-only here; use raw apply for its actions", name)
	}
	if a.Risky && !confirmRisky {
		return "", fmt.Errorf("area %q can drop connectivity; confirm with -yes", name)
	}
	return b.applyFields(a.Page, a.Cmd, changes)
}

// applyFields resolves logical field names to their numeric IDs on the page,
// then submits the change via apply_abstract.cgi.
func (b *Box) applyFields(page, cmd string, nameVals map[string]string) (string, error) {
	cfg, _, err := b.c.PageData(page)
	if err != nil {
		return "", err
	}
	byID := map[string]string{}
	for name, val := range nameVals {
		f, ok := cfg.Lookup(name)
		if !ok {
			return "", fmt.Errorf("setting %q not found on %s", name, page)
		}
		byID[f.ID] = val
	}
	return b.c.Apply(cmd, page, byID)
}

// --- LED ---

var ledStatusRe = regexp.MustCompile(`led_status\s*=\s*"([^"]*)"`)

func (b *Box) LEDStatus() ([]saltbox.LEDNode, error) {
	js, err := b.c.GetCGI("led", "led_ctrl.htm")
	if err != nil {
		return nil, err
	}
	m := ledStatusRe.FindStringSubmatch(js)
	if m == nil {
		return nil, fmt.Errorf("led_status not found")
	}
	var out []saltbox.LEDNode
	for _, e := range strings.Split(m[1], ",") {
		p := strings.Split(e, "@")
		if len(p) < 3 {
			continue
		}
		lvl, _ := strconv.Atoi(p[2])
		out = append(out, saltbox.LEDNode{MAC: p[0], Name: p[1], Level: lvl})
	}
	return out, nil
}

func (b *Box) SetLED(target string, level int) error {
	if target == "" {
		target = "ALL"
	}
	body, err := b.c.PostSimple("/owl_cmd.cgi", "led_ctrl.htm", map[string]string{
		"cmd": "setLED", "target": target, "led_brightness": strconv.Itoa(level),
	})
	if err != nil {
		return err
	}
	if !strings.Contains(body, `"OK"`) {
		return fmt.Errorf("unexpected response: %s", strings.TrimSpace(body))
	}
	return nil
}

// --- Wi-Fi ---

func bandSuffix(band string) (string, error) {
	switch band {
	case "24", "2.4":
		return "24G", nil
	case "5":
		return "5G", nil
	}
	return "", fmt.Errorf("band must be 24 or 5, got %q", band)
}

func (b *Box) wifiArea(band string) (areaDef, error) {
	switch band {
	case "24", "2.4":
		a, _ := b.areaByName("wifi24")
		return a, nil
	case "5":
		a, _ := b.areaByName("wifi5")
		return a, nil
	}
	return areaDef{}, fmt.Errorf("band must be 24 or 5, got %q", band)
}

func (b *Box) SetRadio(band string, on bool) error {
	a, err := b.wifiArea(band)
	if err != nil {
		return err
	}
	s, _ := bandSuffix(band)
	_, err = b.applyFields(a.Page, a.Cmd, map[string]string{"Wireless_enable" + s: boolStr(on)})
	return err
}

func (b *Box) SetSSID(band, ssid string) error {
	a, err := b.wifiArea(band)
	if err != nil {
		return err
	}
	s, _ := bandSuffix(band)
	_, err = b.applyFields(a.Page, a.Cmd, map[string]string{"wl_ssid" + s: ssid})
	return err
}

func (b *Box) SetWiFiPassword(band, pw string) error {
	a, err := b.wifiArea(band)
	if err != nil {
		return err
	}
	s, _ := bandSuffix(band)
	_, err = b.applyFields(a.Page, a.Cmd, map[string]string{"sharedkey" + s: pw})
	return err
}

func (b *Box) SetChannel(band string, channel int) error {
	a, err := b.wifiArea(band)
	if err != nil {
		return err
	}
	s, _ := bandSuffix(band)
	fields := map[string]string{}
	if channel <= 0 {
		fields["auto_chan"+s] = "1"
	} else {
		fields["auto_chan"+s] = "0"
		fields["wchan"+s] = strconv.Itoa(channel)
	}
	_, err = b.applyFields(a.Page, a.Cmd, fields)
	return err
}

// --- Guest Wi-Fi (primary SSID per band) ---

func (b *Box) SetGuest(on bool) error {
	a, _ := b.areaByName("guest")
	_, err := b.applyFields(a.Page, a.Cmd, map[string]string{
		"ssid_enable24G1": boolStr(on), "ssid_enable5G1": boolStr(on),
	})
	return err
}

func (b *Box) SetGuestSSID(band, ssid string) error {
	s, err := bandSuffix(band)
	if err != nil {
		return err
	}
	a, _ := b.areaByName("guest")
	_, err = b.applyFields(a.Page, a.Cmd, map[string]string{"wl_ssid" + s + "1": ssid})
	return err
}

func (b *Box) SetGuestPassword(band, pw string) error {
	s, err := bandSuffix(band)
	if err != nil {
		return err
	}
	a, _ := b.areaByName("guest")
	_, err = b.applyFields(a.Page, a.Cmd, map[string]string{"sharedkey" + s + "1": pw})
	return err
}

// --- DECT ---

func (b *Box) SetDECT(on bool) error {
	_, err := b.applyFields("tel_dect.htm", "ui_dect_onoff", map[string]string{"dect_func": boolStr(on)})
	return err
}

func (b *Box) SetDECTEco(on bool) error {
	_, err := b.applyFields("tel_dect.htm", "ui_dect_onoff", map[string]string{"dect_emission_mode": boolStr(on)})
	return err
}

// --- DDNS ---

func (b *Box) SetDDNS(enable bool, service, host, user, pass string) error {
	fields := map[string]string{"ddns_enable": boolStr(enable)}
	if service != "" {
		fields["ddns_service"] = service
	}
	if host != "" {
		fields["ddns_hostname"] = host
	}
	if user != "" {
		fields["ddns_username"] = user
	}
	if pass != "" {
		fields["ddns_password"] = pass
	}
	_, err := b.applyFields("dynamic_dns.htm", "ui_ddns", fields)
	return err
}

// --- DMZ ---

func (b *Box) SetDMZ(host string) error {
	fields := map[string]string{}
	if host == "" {
		fields["dmz_enable"] = "0"
	} else {
		fields["dmz_enable"] = "1"
		fields["host_ip"] = host
	}
	_, err := b.applyFields("firewall_dmz.htm", "ui_firewall", fields)
	return err
}

// --- UPnP ---

func (b *Box) SetUPnP(on bool) error {
	_, err := b.applyFields("upnp.htm", "ui_upnp", map[string]string{"upnp_enable": boolStr(on)})
	return err
}

// --- mesh / reboot ---

func (b *Box) MeshAction(action string) (string, error) {
	var cmd string
	switch action {
	case "optimize":
		cmd = "getdiscoptimising"
	case "reboot":
		cmd = "reboot"
	case "reset":
		cmd = "reset"
	default:
		return "", fmt.Errorf("mesh action must be optimize|reboot|reset")
	}
	return b.c.PostSimple("/owl_cmd.cgi", "owl_discs.htm", map[string]string{"cmd": cmd})
}

func (b *Box) Reboot() (string, error) {
	return b.c.Apply("reboot", "system_r.htm", nil)
}

// --- config backup / restore ---

var bkupFileRe = regexp.MustCompile(`cgi_bkup_file='([^']+)'`)

// ConfigSave downloads the box's encrypted configuration blob. Loading
// cgi_sys_bk.js makes the box generate it under /tmp; it is then fetched with
// the same page token.
func (b *Box) ConfigSave() (io.ReadCloser, string, error) {
	tok, ms, err := b.c.FreshToken("system_backup.htm")
	if err != nil {
		return nil, "", err
	}
	_, _, js, err := b.c.GetRaw("/cgi/cgi_sys_bk.js?_tn="+tok+"&_t="+ms, "system_backup.htm")
	if err != nil {
		return nil, "", err
	}
	m := bkupFileRe.FindSubmatch(js)
	if m == nil {
		return nil, "", fmt.Errorf("backup filename not found")
	}
	fn := string(m[1])
	ms2 := fmt.Sprint(time.Now().UnixMilli())
	body, code, err := b.c.GetStream("/tmp/"+fn+"?_tn="+tok+"&_t="+ms2, "system_backup.htm")
	if err != nil {
		return nil, "", err
	}
	if code != 200 {
		body.Close()
		return nil, "", fmt.Errorf("download %s: HTTP %d", fn, code)
	}
	return body, fn, nil
}

// ConfigRestore uploads a previously saved blob and reboots the box.
func (b *Box) ConfigRestore(r io.Reader, name string) (string, error) {
	if name == "" {
		name = "restore.cfg"
	}
	return b.c.UploadConfig("/upload.cgi", "system_backup.htm", "SYS_PROF", "restore", name, r)
}

// --- port forwarding (nat_port_map.htm) ---

const pfRows = 32

func (b *Box) PortForwardList() ([]saltbox.PortForwardRule, error) {
	cfg, _, err := b.c.PageData("nat_port_map.htm")
	if err != nil {
		return nil, err
	}
	var out []saltbox.PortForwardRule
	for i := 0; i < pfRows; i++ {
		s := strconv.Itoa(i)
		proto := cfgVal(cfg, "pm_protocol_"+s)
		if proto != "tcp" && proto != "udp" && proto != "both" {
			continue
		}
		out = append(out, saltbox.PortForwardRule{
			Index:        i,
			Enabled:      cfgVal(cfg, "pm_enable_"+s) == "1",
			Protocol:     proto,
			InternalIP:   cfgVal(cfg, "pm_private_ip_"+s),
			PublicStart:  atoi(cfgVal(cfg, "pm_public_port_start_"+s)),
			PublicEnd:    atoi(cfgVal(cfg, "pm_public_port_end_"+s)),
			PrivateStart: atoi(cfgVal(cfg, "pm_private_port_start_"+s)),
			PrivateEnd:   atoi(cfgVal(cfg, "pm_private_port_end_"+s)),
		})
	}
	return out, nil
}

func (b *Box) PortForwardAdd(r saltbox.PortForwardRule) (int, error) {
	if r.Protocol != "tcp" && r.Protocol != "udp" && r.Protocol != "both" {
		return -1, fmt.Errorf("protocol must be tcp, udp or both")
	}
	if r.InternalIP == "" || r.PublicStart <= 0 {
		return -1, fmt.Errorf("need internal IP and a public start port")
	}
	if r.PublicEnd <= 0 {
		r.PublicEnd = r.PublicStart
	}
	if r.PrivateStart <= 0 {
		r.PrivateStart = r.PublicStart
	}
	if r.PrivateEnd <= 0 {
		r.PrivateEnd = r.PrivateStart + (r.PublicEnd - r.PublicStart)
	}
	cfg, _, err := b.c.PageData("nat_port_map.htm")
	if err != nil {
		return -1, err
	}
	idx := -1
	for i := 0; i < pfRows; i++ {
		if p := cfgVal(cfg, "pm_protocol_"+strconv.Itoa(i)); p != "tcp" && p != "udp" && p != "both" {
			idx = i
			break
		}
	}
	if idx < 0 {
		return -1, fmt.Errorf("no free port-forward slot (max %d)", pfRows)
	}
	s := strconv.Itoa(idx)
	_, err = b.applyFields("nat_port_map.htm", "ui_firewall", map[string]string{
		"pm_enable_" + s:             "1",
		"pm_protocol_" + s:           r.Protocol,
		"pm_private_ip_" + s:         r.InternalIP,
		"pm_public_port_start_" + s:  strconv.Itoa(r.PublicStart),
		"pm_public_port_end_" + s:    strconv.Itoa(r.PublicEnd),
		"pm_private_port_start_" + s: strconv.Itoa(r.PrivateStart),
		"pm_private_port_end_" + s:   strconv.Itoa(r.PrivateEnd),
	})
	return idx, err
}

func (b *Box) PortForwardRemove(index int) error {
	if index < 0 || index >= pfRows {
		return fmt.Errorf("index out of range 0..%d", pfRows-1)
	}
	s := strconv.Itoa(index)
	_, err := b.applyFields("nat_port_map.htm", "ui_firewall", map[string]string{
		"pm_enable_" + s: "0", "pm_protocol_" + s: "", "pm_private_ip_" + s: "",
		"pm_public_port_start_" + s: "", "pm_public_port_end_" + s: "",
		"pm_private_port_start_" + s: "", "pm_private_port_end_" + s: "",
	})
	return err
}

// --- DHCP static reservations (lan_dhcp_static.htm, bank 0) ---

const dhcpSlots = 20

func (b *Box) DHCPList() ([]saltbox.Reservation, error) {
	cfg, _, err := b.c.PageData("lan_dhcp_static.htm")
	if err != nil {
		return nil, err
	}
	var out []saltbox.Reservation
	for i := 0; i < dhcpSlots; i++ {
		s := strconv.Itoa(i)
		mac := cfgVal(cfg, "static_mac_0_"+s)
		if mac == "" {
			continue
		}
		out = append(out, saltbox.Reservation{
			Index:  i,
			MAC:    mac,
			IP:     cfgVal(cfg, "static_ip_0_"+s),
			Name:   cfgVal(cfg, "static_name_0_"+s),
			Active: cfgVal(cfg, "static_active_0_"+s) == "1",
		})
	}
	return out, nil
}

func (b *Box) DHCPReserve(mac, ip, name string) (int, error) {
	if mac == "" || ip == "" {
		return -1, fmt.Errorf("need a MAC and an IP")
	}
	cfg, _, err := b.c.PageData("lan_dhcp_static.htm")
	if err != nil {
		return -1, err
	}
	idx := -1
	for i := 0; i < dhcpSlots; i++ {
		if cfgVal(cfg, "static_mac_0_"+strconv.Itoa(i)) == "" {
			idx = i
			break
		}
	}
	if idx < 0 {
		return -1, fmt.Errorf("no free reservation slot (max %d)", dhcpSlots)
	}
	s := strconv.Itoa(idx)
	_, err = b.applyFields("lan_dhcp_static.htm", "ui_dhcp_static", map[string]string{
		"static_enable":        "1",
		"static_mac_0_" + s:    mac,
		"static_ip_0_" + s:     ip,
		"static_name_0_" + s:   name,
		"static_active_0_" + s: "1",
	})
	return idx, err
}

func (b *Box) DHCPRemove(index int) error {
	if index < 0 || index >= dhcpSlots {
		return fmt.Errorf("index out of range 0..%d", dhcpSlots-1)
	}
	s := strconv.Itoa(index)
	_, err := b.applyFields("lan_dhcp_static.htm", "ui_dhcp_static", map[string]string{
		"static_mac_0_" + s: "", "static_ip_0_" + s: "",
		"static_name_0_" + s: "", "static_active_0_" + s: "0",
	})
	return err
}

func cfgVal(c *arcadyan.Config, name string) string {
	if f, ok := c.Lookup(name); ok {
		return f.Value
	}
	return ""
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// --- raw ---

func (b *Box) RawGet(cgi, page string) (string, error) { return b.c.GetCGI(cgi, page) }
func (b *Box) RawPost(endpoint, page string, fields map[string]string) (string, error) {
	return b.c.PostSimple(endpoint, page, fields)
}
func (b *Box) RawApply(cmd, page string, fields map[string]string) (string, error) {
	return b.c.Apply(cmd, page, fields)
}

func boolStr(on bool) string {
	if on {
		return "1"
	}
	return "0"
}
