# Feature map (Salt Fiber Box, firmware v1.01.42)

Built from a live survey of the box's web UI. Each **area** is a settings page
exposed by `saltctl`; read it with `saltctl get <area>` and (where writable)
change it with `saltctl set <area> <field>=<value>`. Field names come straight
from the page (`saltctl get <area>` lists them with their current values).

Two write mechanisms are reproduced faithfully:

- **`owl_cmd.cgi`** — simple plaintext command + `httoken` (LED, mesh actions).
  Proven: `led off/on`.
- **`apply_abstract.cgi`** — `action` and each changed field AES‑256‑CBC
  encrypted (key/IV from the page's token image). Proven: a UPnP toggle
  (`set upnp upnp_enable=1` → verified → restored).

## Areas (accessible with the admin/expert account)

| Area | Page | Access | Apply cmd | Notes |
|---|---|---|---|---|
| `state` | state.htm | read | — | PON + telephony + Wi‑Fi summary |
| `modem` | router_status.htm | read | — | model / firmware / serial (`status`) |
| `pon` | pon_status.htm | read | — | fiber / GPON status |
| `devices` | clients.htm | read | — | connected devices (DHCP + MAC) |
| `lte` | lte_status.htm | read | — | LTE status |
| `mesh` | owl_status.htm | read | — | Smart Wi‑Fi topology + LED |
| `extenders` | owl_discs.htm | read | — | mesh extenders |
| `wifi` | wlan_combine1.htm | **rw** | ui_wireless_combine | combined Smart Wi‑Fi ⚠️ |
| `wifi24` | wlan_24g1.htm | **rw** | ui_wireless | 2.4 GHz radio ⚠️ |
| `wifi5` | wlan_5g1.htm | **rw** | ui_wireless | 5 GHz radio ⚠️ |
| `guest` | wlan_guest.htm | **rw** | ui_wireless_guest | guest Wi‑Fi |
| `wifi-macfilter` | wlan_mac_filter.htm | **rw** | ui_wireless | Wi‑Fi MAC filter |
| `lan` | lan.htm | **rw** | ui_lan | LAN IP + DHCP server ⚠️ |
| `dhcp-reservations` | lan_dhcp_static.htm | **rw** | ui_dhcp_static | DHCP static leases |
| `nat` | nat.htm | **rw** | ui_rip | NAT settings |
| `portforward` | nat_port_map.htm | **rw** | ui_firewall | port forwarding |
| `special-app` | nat_sp.htm | **rw** | ui_firewall | NAT special applications |
| `ddns` | dynamic_dns.htm | **rw** | ui_ddns | dynamic DNS |
| `upnp` | upnp.htm | **rw** | ui_upnp | UPnP |
| `firewall` | firewall_main.htm | **rw** | ui_firewall | main switches ⚠️ |
| `firewall-ac` | firewall_ac.htm | **rw** | ui_firewall | access control |
| `firewall-url` | firewall_url.htm | **rw** | ui_firewall | URL blocking |
| `firewall-dmz` | firewall_dmz.htm | **rw** | ui_firewall | DMZ host ⚠️ |
| `firewall-spi` | firewall_spi_h.htm | **rw** | ui_firewall | SPI firewall |
| `ipv6-firewall` | ipv6_firewall.htm | **rw** | ui_firewall6 | IPv6 firewall ⚠️ |
| `dect` | tel_dect.htm | read | *(multi)* | DECT; writes via `raw apply` (see below) |
| `usb` | usb_basic.htm | read | — | USB status |
| `usb-media` | usb_media.htm | **rw** | ui_usb_mediaserver | DLNA media server |
| `power-saving` | power_saving.htm | **rw** | ui_ECOmode | eco / Wi‑Fi & DECT scheduling |

⚠️ = can drop internet/LAN/Wi‑Fi; `set` refuses these without `-yes`.

## Simple actions (not `get`/`set`)

| Command | Endpoint | Status |
|---|---|---|
| `led off / dim / on / set N` | owl_cmd.cgi `setLED` | ✅ tested |
| `wifi on / off -yes` | apply `ui_wireless` (both radios) | implemented |
| `mesh optimize / reboot / reset` | owl_cmd.cgi | implemented |
| `reboot -yes` | apply `reboot` | implemented |

## Multi‑action pages (use `raw apply`)

Some pages drive several commands rather than one settings form, so they aren't
single `set` areas. Use `raw apply <cmd> -page <page> field=value` with the cmd
from the UI:

- **DECT** (`tel_dect.htm`): `ui_dect_onoff`, `ui_dect_base_name`,
  `ui_dect_base_pin`, `ui_dect_eco_range`, `ui_dect_security`, `ui_dect_reg`…
- **USB** (`usb_*`): `ui_usb_sharing`, `ui_usb_account`, `ui_usb_unmount`…
- **Password** (`system_p.htm`): posts to `setpwd.cgi`.
- **Factory reset** (`system_backup.htm`): `ui_factory_defaults` — destructive.

## Not available with this account (HTTP 403, "super" only)

`status` (super network status), security log, routing (static/RIP), TFTP
config, time settings, remote management, firmware upgrade, feedback/problem
solving, bandwidth test, the built‑in `curl` tool. These need a higher‑privilege
account than `admin`, so `saltctl` can't reach them.

## Examples

```sh
saltctl areas                       # list every area
saltctl get lan                     # LAN IP + DHCP settings
saltctl get firewall                # firewall switches
saltctl set ddns ddns_enable=1 ddns_service=dyndns ddns_hostname=home.example
saltctl set portforward <field>=<value>
saltctl set wifi24 Wireless_enable24G=0 -yes     # turn 2.4 GHz off
saltctl led off
```
