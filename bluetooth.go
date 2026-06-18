package main

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/godbus/dbus/v5"
)

const (
	hidUUID     = "00001124-0000-1000-8000-00805f9b34fb"
	profilePath = "/org/bluez/switchbtprocon/hid"
	hidSDP      = `<?xml version="1.0" encoding="UTF-8" ?>
<record>
    <attribute id="0x0001"><sequence><uuid value="0x1124"/></sequence></attribute>
    <attribute id="0x0004"><sequence><sequence><uuid value="0x0100"/><uint16 value="0x0011"/></sequence><sequence><uuid value="0x0011"/></sequence></sequence></attribute>
    <attribute id="0x0005"><sequence><uuid value="0x1002"/></sequence></attribute>
    <attribute id="0x0006"><sequence><uint16 value="0x656e"/><uint16 value="0x006a"/><uint16 value="0x0100"/></sequence></attribute>
    <attribute id="0x0009"><sequence><sequence><uuid value="0x1124"/><uint16 value="0x0100"/></sequence></sequence></attribute>
    <attribute id="0x000d"><sequence><sequence><sequence><uuid value="0x0100"/><uint16 value="0x0013"/></sequence><sequence><uuid value="0x0011"/></sequence></sequence></sequence></attribute>
    <attribute id="0x0100"><text value="Wireless Gamepad"/></attribute>
    <attribute id="0x0101"><text value="Gamepad"/></attribute>
    <attribute id="0x0102"><text value="Nintendo"/></attribute>
    <attribute id="0x0200"><uint16 value="0x0100"/></attribute>
    <attribute id="0x0201"><uint16 value="0x0111"/></attribute>
    <attribute id="0x0202"><uint8 value="0x08"/></attribute>
    <attribute id="0x0203"><uint8 value="0x00"/></attribute>
    <attribute id="0x0204"><boolean value="true"/></attribute>
    <attribute id="0x0205"><boolean value="true"/></attribute>
    <attribute id="0x0206"><sequence><sequence><uint8 value="0x22"/><text encoding="hex" value="050115000904a1018530050105091901290a150025017501950a5500650081020509190b290e150025017501950481027501950281030b01000100a1000b300001000b310001000b320001000b35000100150027ffff0000751095048102c00b39000100150025073500463b0165147504950181020509190f2912150025017501950481027508953481030600ff852109017508953f8103858109027508953f8103850109037508953f9183851009047508953f9183858009057508953f9183858209067508953f9183c0"/></sequence></sequence></attribute>
    <attribute id="0x0207"><sequence><sequence><uint16 value="0x0409"/><uint16 value="0x0100"/></sequence></sequence></attribute>
    <attribute id="0x020b"><uint16 value="0x0100"/></attribute>
    <attribute id="0x020c"><uint16 value="0x0c80"/></attribute>
    <attribute id="0x020d"><boolean value="false"/></attribute>
    <attribute id="0x020e"><boolean value="true"/></attribute>
    <attribute id="0x020f"><uint16 value="0x0640"/></attribute>
    <attribute id="0x0210"><uint16 value="0x0320"/></attribute>
</record>`
)

type btAdapter struct {
	conn    *dbus.Conn
	path    dbus.ObjectPath
	name    string
	address string
}

func openAdapter(selector string) (*btAdapter, error) {
	conn, err := dbus.SystemBus()
	if err != nil {
		return nil, err
	}

	var managed map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err = conn.Object("org.bluez", "/").Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&managed)
	if err != nil {
		return nil, err
	}

	for path, ifaces := range managed {
		props, ok := ifaces["org.bluez.Adapter1"]
		if !ok {
			continue
		}
		address, _ := props["Address"].Value().(string)
		name := filepath.Base(string(path))
		if selector == "" || selector == address || selector == name {
			return &btAdapter{
				conn:    conn,
				path:    path,
				name:    name,
				address: address,
			}, nil
		}
	}
	return nil, fmt.Errorf("bluetooth adapter %q not found", selector)
}

func (a *btAdapter) Close() error {
	return a.conn.Close()
}

func (a *btAdapter) setProp(name string, value any) error {
	obj := a.conn.Object("org.bluez", a.path)
	return obj.Call("org.freedesktop.DBus.Properties.Set", 0, "org.bluez.Adapter1", name, dbus.MakeVariant(value)).Err
}

func (a *btAdapter) Powered(v bool) error      { return a.setProp("Powered", v) }
func (a *btAdapter) Pairable(v bool) error     { return a.setProp("Pairable", v) }
func (a *btAdapter) Discoverable(v bool) error { return a.setProp("Discoverable", v) }
func (a *btAdapter) Alias(v string) error      { return a.setProp("Alias", v) }

func (a *btAdapter) SetClass(class string) error {
	return exec.Command("hciconfig", a.name, "class", class).Run()
}

func (a *btAdapter) RegisterProfile() error {
	_ = a.UnregisterProfile()
	opts := map[string]dbus.Variant{
		"ServiceRecord":         dbus.MakeVariant(hidSDP),
		"Role":                  dbus.MakeVariant("server"),
		"Service":               dbus.MakeVariant(hidUUID),
		"RequireAuthentication": dbus.MakeVariant(false),
		"RequireAuthorization":  dbus.MakeVariant(false),
	}
	err := a.conn.Object("org.bluez", "/org/bluez").Call(
		"org.bluez.ProfileManager1.RegisterProfile",
		0,
		dbus.ObjectPath(profilePath),
		hidUUID,
		opts,
	).Err
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "already exists") || strings.Contains(msg, "already registered") {
			return nil
		}
	}
	if err != nil && strings.Contains(err.Error(), "Already Exists") {
		return nil
	}
	return err
}

func (a *btAdapter) UnregisterProfile() error {
	err := a.conn.Object("org.bluez", "/org/bluez").Call(
		"org.bluez.ProfileManager1.UnregisterProfile",
		0,
		dbus.ObjectPath(profilePath),
	).Err
	if err != nil && (strings.Contains(err.Error(), "Does Not Exist") || strings.Contains(err.Error(), "UnknownObject")) {
		return nil
	}
	return err
}

func (a *btAdapter) TrustDevice(deviceAddr string) error {
	_, err := parseBTAddress(deviceAddr)
	if err != nil {
		return err
	}
	return exec.Command("bluetoothctl", "trust", deviceAddr).Run()
}

func parseBTAddress(addr string) ([6]byte, error) {
	var out [6]byte
	parts := strings.Split(strings.TrimSpace(addr), ":")
	if len(parts) != 6 {
		return out, errors.New("invalid bluetooth address")
	}
	for i, p := range parts {
		var b byte
		_, err := fmt.Sscanf(p, "%02X", &b)
		if err != nil {
			_, err = fmt.Sscanf(strings.ToUpper(p), "%02X", &b)
		}
		if err != nil {
			var v uint8
			_, err = fmt.Sscanf(p, "%x", &v)
			if err != nil {
				return out, err
			}
			b = byte(v)
		}
		out[i] = b
	}
	return out, nil
}

func formatBTAddress(raw [6]byte) string {
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", raw[5], raw[4], raw[3], raw[2], raw[1], raw[0])
}
