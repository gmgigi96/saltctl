# saltctl

A small, dependency-free Go CLI for **Salt's internet boxes**. It reproduces
the browser's login and request signing so a box can be driven from scripts
and schedulers — the original motivation being *turn the status LEDs off at
night*.

Today it targets the **Salt Fiber Box** (Arcadyan "Saltbox 1 A 1.0",
`BCM68580`) at `http://192.168.1.1`. The other Salt fibre boxes — **Fiber Box
X6 / X6 Pro / Wi‑Fi 7+** — share the same Arcadyan "WebBase" UI and should
work with little or no change; the 5G **GigaBox** is different hardware and
would need its own backend. Contributions for those are welcome.

## How it talks to the box

The firmware is Arcadyan's "WebBase" stack. There is no clean REST API; the CLI
mimics exactly what the web UI's JavaScript does:

- **Login** (`POST /login.cgi`): username and password are each sent as
  `SHA512( hex(MD5(x)) )` (the UI's `do_encode(x,true)` / `ArcMD5`). Success
  sets a session `SID` cookie.
- **Per-request token**: every page embeds a spacer `<img src="data:…">`. Its
  base64 payload (from offset 78) is `key[0:32] ‖ iv[32:48] ‖ httoken[48:]`.
  The `httoken` must accompany every state-changing request and every
  `cgi/cgi_*.js` read (`?_tn=<token>&_t=<ms>`); tokens are per page-load, so
  the CLI loads a page immediately before each action.
- **Reads** (`GET /cgi/cgi_<name>.js`): return `var NAME="…";` assignments.
- **LED control** (`POST /owl_cmd.cgi`): `cmd=setLED&target=ALL&led_brightness=<0|1|2>`
  → `{ "result": "OK" }`. `0=Off, 1=Dim, 2=Normal`.
- **Settings writes** (`POST /apply_abstract.cgi`): `action` and each changed
  field are **AES-256-CBC** encrypted (key/iv = the ASCII bytes above,
  zero-padded, rendered `%xx%xx…`), plus `httoken` and `submit_button=<page>`.

See `internal/arcadyan/` for the exact reproduction.

## Architecture

The code is structured so other Salt boxes can be added later without touching
the CLI:

```
main.go                         the CLI (flags, subcommands)
internal/arcadyan/              Arcadyan "WebBase" protocol (model-agnostic):
                                login, token, AES signing, get/apply, config
internal/saltbox/               Device + capability interfaces + model registry
internal/saltbox/fiberbox/      the Salt Fiber Box model (areas + features)
```

A model registers itself (`saltbox.Register`) and implements `Device` plus the
capability interfaces it supports (`LED`, `WiFi`, `DDNS`, `Settings`, …). The
CLI selects a model (`-model`, default `fiberbox`) and type-asserts the
capability each command needs. Adding the Fiber Box X6 or the 5G GigaBox later
is a new package under `internal/saltbox/` that registers itself — no CLI
changes. **`fiberbox` is the only supported model today.**

## Build

```sh
go build -o saltctl .
# for andromeda (always-on, on the LAN):
GOOS=linux GOARCH=arm64 go build -o saltctl-linux-arm64 .
```

## Credentials

The admin password is supplied (never baked in) via, in order of precedence:

1. `-password <pw>`
2. `-password-file <path>`
3. `$SALTCTL_PASSWORD`

`-url` (`$SALTCTL_URL`, default `http://192.168.1.1`) and `-user`
(`$SALTCTL_USER`, default `admin`) select the target.

## Usage

```sh
export SALTCTL_PASSWORD='…'

saltctl login                       # verify credentials
saltctl status                      # model / firmware / serial
saltctl devices                     # connected devices

# LED
saltctl led off | dim | on          # 0 / 1 / 2
saltctl led get

# Wi-Fi (friendly wrappers; -band 24|5|both, default both)
saltctl wifi ssid MyHome
saltctl wifi password 's3cret-pass'
saltctl wifi channel 36 -band 5
saltctl wifi off -yes               # disable radios (needs -yes)
saltctl guest on
saltctl guest ssid Visitors

# services
saltctl ddns set -service dyndns -host home.example -user u -pass p
saltctl dmz set 192.168.1.50 -yes   # expose a host (needs -yes)
saltctl dmz off
saltctl upnp on | off
saltctl dect on | off
saltctl mesh optimize               # reboot|reset need -yes
saltctl reboot -yes
saltctl config save -o home.cfg     # download the (encrypted) config backup
saltctl config restore home.cfg -yes

# generic layer — a command for every page (see FEATURES.md)
saltctl areas
saltctl get firewall
saltctl set portforward pm_enable_0=1 pm_protocol_0=tcp pm_private_ip_0=192.168.1.50 \
    pm_private_port_start_0=80 pm_public_port_start_0=8080

# escape hatch — anything not wrapped
saltctl raw apply ui_factory_defaults -page system_backup.htm
```

See **[FEATURES.md](FEATURES.md)** for the full area/feature map, which pages
are gated behind a higher-privilege account, and the multi-action pages (DECT,
USB, password, factory reset) that use `raw apply`.

### Tested vs. spec-only

- **Tested against the live box:** `login`, `models`, `led get/off/dim/on/set`,
  `status`, `devices`, `areas`, `get <area>` across areas, **the AES write path
  (`set` / `upnp on`/`off`) verified via a reversible UPnP toggle**,
  `config save` (downloads the encrypted blob), `raw get/post`.
- **Implemented from the UI's JS but not exercised here** (they change live
  config / restart service): `reboot`, `config restore`, `wifi on/off`,
  `mesh reboot/reset`, and `set`/`raw apply` on connectivity-affecting areas.
  Try these deliberately; risky areas require `-yes`.

## Scheduling (lights off at night)

`saltctl` is stateless — it logs in afresh each run — so any scheduler
works. Examples:

```sh
# crontab on an always-on LAN host (e.g. andromeda). Zurich time.
# off at 23:00, back to normal at 07:00
0 23 * * *  SALTCTL_PASSWORD=$(cat /etc/saltctl/pw) /usr/local/bin/saltctl led off
0  7 * * *  SALTCTL_PASSWORD=$(cat /etc/saltctl/pw) /usr/local/bin/saltctl led on
```

A systemd timer or a Kubernetes `CronJob` work equally well; the password
belongs in a root-only file / Secret, not in the unit or manifest.

## Security notes

- The box's admin UI is HTTP-only on the LAN and has no real API; treat the
  password as a LAN credential and store it encrypted at rest.
- `reboot`, `raw apply`, and factory-reset-style commands can disrupt service
  — the CLI guards `reboot` behind `-yes` and does not wrap destructive
  settings as first-class commands by design.

## License

[MIT](LICENSE) © 2026 Gianmaria Del Monte.
