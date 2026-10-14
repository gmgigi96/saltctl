package main

// An area is a settings page exposed as a friendly name. Reads work for any
// area (the page's cgi data files are fetched and parsed). Writes work when
// Cmd is set (the apply_abstract.cgi command for that page). Risky marks areas
// whose changes can drop internet/LAN/Wi-Fi connectivity.
type area struct {
	Name  string
	Page  string // .htm page (referer + submit_button + token source)
	Cmd   string // apply_abstract.cgi action; "" => read-only here
	Risky bool
	Desc  string
}

// Derived from a live survey of the box + subformvar.js. Pages gated behind a
// higher "super" account (routing, logs, time, firmware, remote-mgmt, …) are
// intentionally omitted: the admin/expert account returns 403 for them.
var areas = []area{
	// --- read-only status ---
	{"state", "state.htm", "", false, "Overview (PON, telephony, Wi-Fi summary)"},
	{"modem", "router_status.htm", "", false, "Model / firmware / serial"},
	{"pon", "pon_status.htm", "", false, "Fiber / GPON status"},
	{"devices", "clients.htm", "", false, "Connected devices (DHCP + MAC)"},
	{"lte", "lte_status.htm", "", false, "LTE status"},
	{"mesh", "owl_status.htm", "", false, "Smart Wi-Fi mesh topology + LED"},
	{"extenders", "owl_discs.htm", "", false, "Mesh extenders"},

	// --- Wi-Fi ---
	{"wifi", "wlan_combine1.htm", "ui_wireless_combine", true, "Smart Wi-Fi (combined 2.4/5 GHz)"},
	{"wifi24", "wlan_24g1.htm", "ui_wireless", true, "2.4 GHz Wi-Fi radio"},
	{"wifi5", "wlan_5g1.htm", "ui_wireless", true, "5 GHz Wi-Fi radio"},
	{"guest", "wlan_guest.htm", "ui_wireless_guest", false, "Guest Wi-Fi"},
	{"wifi-macfilter", "wlan_mac_filter.htm", "ui_wireless", false, "Wi-Fi MAC filter"},

	// --- LAN / routing-ish ---
	{"lan", "lan.htm", "ui_lan", true, "LAN IP + DHCP server"},
	{"dhcp-reservations", "lan_dhcp_static.htm", "ui_dhcp_static", false, "DHCP static leases"},
	{"nat", "nat.htm", "ui_rip", false, "NAT settings"},
	{"portforward", "nat_port_map.htm", "ui_firewall", false, "Port forwarding"},
	{"special-app", "nat_sp.htm", "ui_firewall", false, "NAT special applications"},
	{"ddns", "dynamic_dns.htm", "ui_ddns", false, "Dynamic DNS"},
	{"upnp", "upnp.htm", "ui_upnp", false, "UPnP"},

	// --- firewall ---
	{"firewall", "firewall_main.htm", "ui_firewall", true, "Firewall main switches"},
	{"firewall-ac", "firewall_ac.htm", "ui_firewall", false, "Firewall access control"},
	{"firewall-url", "firewall_url.htm", "ui_firewall", false, "URL blocking"},
	{"firewall-dmz", "firewall_dmz.htm", "ui_firewall", true, "DMZ host"},
	{"firewall-spi", "firewall_spi_h.htm", "ui_firewall", false, "SPI firewall"},
	{"ipv6-firewall", "ipv6_firewall.htm", "ui_firewall6", true, "IPv6 firewall"},

	// --- telephony / usb (read; writes are multi-action, use `raw apply`) ---
	{"dect", "tel_dect.htm", "", false, "DECT telephony (read; multi-action writes via raw)"},
	{"usb", "usb_basic.htm", "", false, "USB status"},
	{"usb-media", "usb_media.htm", "ui_usb_mediaserver", false, "USB media (DLNA) server"},

	// --- power saving ---
	{"power-saving", "power_saving.htm", "ui_ECOmode", false, "Eco / Wi-Fi & DECT power scheduling"},
}

func findArea(name string) (area, bool) {
	for _, a := range areas {
		if a.Name == name {
			return a, true
		}
	}
	return area{}, false
}
