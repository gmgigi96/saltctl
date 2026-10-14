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

See `crypto.go` and `client.go` for the exact reproduction.

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

saltctl login                 # verify credentials
saltctl led get               # current brightness per node
saltctl led off               # LEDs off   (brightness 0)
saltctl led dim               # LEDs dim   (brightness 1)
saltctl led on                # LEDs normal(brightness 2)
saltctl led set 0 -target ALL # explicit level / node

saltctl status                # model / firmware / serial …
saltctl devices               # connected-device data
saltctl reboot -yes           # reboot (drops internet & phone ~60s)

# escape hatch — any page/endpoint in the UI:
saltctl raw page firewall_main.htm
saltctl raw get led -page led_ctrl.htm
saltctl raw post owl_cmd.cgi -page led_ctrl.htm cmd=setLED target=ALL led_brightness=0
saltctl raw apply <cmd> -page <page.htm> field=value …
```

### Tested vs. spec-only

- **Tested against the live box:** `login`, `led get/off/dim/on/set`,
  `status`, `raw get/page`, `raw post`.
- **Implemented from the UI's JS but not exercised here** (they change live
  config / restart service): `reboot` and the generic `raw apply` AES path.
  Try these deliberately.

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
