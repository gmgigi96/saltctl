// Package saltbox defines the device abstraction for Salt's internet boxes.
//
// A concrete model (e.g. the Fiber Box) registers a factory and implements
// Device plus whichever capability interfaces it supports. The CLI selects a
// model, then type-asserts the capabilities it needs — so adding another Salt
// box later (a different fibre model, or the 5G GigaBox with its own protocol)
// means a new package that registers itself, with no CLI changes.
package saltbox

import (
	"fmt"
	"sort"
)

// Options configures a device connection.
type Options struct {
	URL  string
	User string
	Pass string
}

// Factory builds a Device for a model.
type Factory func(Options) (Device, error)

var registry = map[string]Factory{}

// Register adds a model factory (called from a model package's init).
func Register(model string, f Factory) { registry[model] = f }

// Models lists registered model names.
func Models() []string {
	out := make([]string, 0, len(registry))
	for m := range registry {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// Open constructs a device for the named model.
func Open(model string, o Options) (Device, error) {
	f, ok := registry[model]
	if !ok {
		return nil, fmt.Errorf("unknown model %q (known: %v)", model, Models())
	}
	return f(o)
}

// Device is the minimum every Salt box supports.
type Device interface {
	Model() string
	Login() error
}

// --- capability interfaces (a model implements what it supports) ---

// Area is a settings page exposed under a friendly name.
type Area struct {
	Name     string
	Desc     string
	Writable bool
	Risky    bool
}

// Setting is one name=value pair (editable field or read-only var).
type Setting struct{ Name, Value string }

// Settings is the generic read/write layer over the box's pages.
type Settings interface {
	Areas() []Area
	GetArea(name string) (fields []Setting, reads map[string][]Setting, err error)
	SetArea(name string, changes map[string]string, confirmRisky bool) (string, error)
}

// LEDNode is one node's LED state (0 off, 1 dim, 2 normal).
type LEDNode struct {
	Name  string
	MAC   string
	Level int
}

type LED interface {
	LEDStatus() ([]LEDNode, error)
	SetLED(target string, level int) error
}

// WiFi band is "24" or "5".
type WiFi interface {
	SetRadio(band string, on bool) error
	SetSSID(band, ssid string) error
	SetWiFiPassword(band, pw string) error
	SetChannel(band string, channel int) error // 0 = auto
}

type Guest interface {
	SetGuest(on bool) error
	SetGuestSSID(band, ssid string) error
	SetGuestPassword(band, pw string) error
}

type DECT interface {
	SetDECT(on bool) error
	SetDECTEco(on bool) error
}

type DDNS interface {
	SetDDNS(enable bool, service, host, user, pass string) error
}

type DMZ interface {
	SetDMZ(host string) error // "" disables
}

type UPnP interface {
	SetUPnP(on bool) error
}

type Mesh interface {
	MeshAction(action string) (string, error) // optimize | reboot | reset
}

type Rebooter interface {
	Reboot() (string, error)
}

// Backup saves and restores the box's (encrypted) configuration blob.
type Backup interface {
	ConfigSave() (data []byte, filename string, err error)
	ConfigRestore(data []byte, filename string) (string, error)
}

// Raw is the escape hatch for anything not wrapped.
type Raw interface {
	RawGet(cgi, page string) (string, error)
	RawPost(endpoint, page string, fields map[string]string) (string, error)
	RawApply(cmd, page string, fields map[string]string) (string, error)
}
