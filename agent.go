package main

import (
	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

const (
	agentPath         = dbus.ObjectPath("/org/bluez/switchbtprocon/agent")
	agentInterface    = "org.bluez.Agent1"
	agentIntrospecXML = `<!DOCTYPE node PUBLIC "-//freedesktop//DTD D-BUS Object Introspection 1.0//EN"
"http://www.freedesktop.org/standards/dbus/1.0/introspect.dtd">
<node>
	<interface name="org.bluez.Agent1">
		<method name="Release"/>
		<method name="RequestPinCode">
			<arg type="o" name="device" direction="in"/>
			<arg type="s" name="pincode" direction="out"/>
		</method>
		<method name="DisplayPinCode">
			<arg type="o" name="device" direction="in"/>
			<arg type="s" name="pincode" direction="in"/>
		</method>
		<method name="RequestPasskey">
			<arg type="o" name="device" direction="in"/>
			<arg type="u" name="passkey" direction="out"/>
		</method>
		<method name="DisplayPasskey">
			<arg type="o" name="device" direction="in"/>
			<arg type="u" name="passkey" direction="in"/>
			<arg type="q" name="entered" direction="in"/>
		</method>
		<method name="RequestConfirmation">
			<arg type="o" name="device" direction="in"/>
			<arg type="u" name="passkey" direction="in"/>
		</method>
		<method name="RequestAuthorization">
			<arg type="o" name="device" direction="in"/>
		</method>
		<method name="AuthorizeService">
			<arg type="o" name="device" direction="in"/>
			<arg type="s" name="uuid" direction="in"/>
		</method>
		<method name="Cancel"/>
	</interface>
	<interface name="org.freedesktop.DBus.Introspectable">
		<method name="Introspect">
			<arg type="s" name="xml" direction="out"/>
		</method>
	</interface>
</node>`
)

type btAgent struct{}

func (a *btAgent) Release() *dbus.Error {
	return nil
}

func (a *btAgent) RequestPinCode(device dbus.ObjectPath) (string, *dbus.Error) {
	return "", dbus.NewError("org.bluez.Error.Rejected", []interface{}{"no input capability"})
}

func (a *btAgent) DisplayPinCode(device dbus.ObjectPath, pincode string) *dbus.Error {
	return nil
}

func (a *btAgent) RequestPasskey(device dbus.ObjectPath) (uint32, *dbus.Error) {
	return 0, dbus.NewError("org.bluez.Error.Rejected", []interface{}{"no input capability"})
}

func (a *btAgent) DisplayPasskey(device dbus.ObjectPath, passkey uint32, entered uint16) *dbus.Error {
	return nil
}

func (a *btAgent) RequestConfirmation(device dbus.ObjectPath, passkey uint32) *dbus.Error {
	return nil
}

func (a *btAgent) RequestAuthorization(device dbus.ObjectPath) *dbus.Error {
	return nil
}

func (a *btAgent) AuthorizeService(device dbus.ObjectPath, uuid string) *dbus.Error {
	return nil
}

func (a *btAgent) Cancel() *dbus.Error {
	return nil
}

func registerAgent(conn *dbus.Conn) error {
	agent := &btAgent{}
	if err := conn.Export(agent, agentPath, agentInterface); err != nil {
		return err
	}
	if err := conn.Export(introspect.Introspectable(agentIntrospecXML), agentPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		return err
	}
	obj := conn.Object("org.bluez", "/org/bluez")
	if err := obj.Call("org.bluez.AgentManager1.RegisterAgent", 0, agentPath, "NoInputNoOutput").Err; err != nil {
		return err
	}
	return obj.Call("org.bluez.AgentManager1.RequestDefaultAgent", 0, agentPath).Err
}

func unregisterAgent(conn *dbus.Conn) {
	_ = conn.Object("org.bluez", "/org/bluez").Call("org.bluez.AgentManager1.UnregisterAgent", 0, agentPath).Err
}
