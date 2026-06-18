# switchbt-procon next steps

## Current state

- The standalone Go daemon in `/home/blikvm/Downloads/switchbt-procon` can now:
  - expose a Bluetooth HID profile through BlueZ
  - pair successfully with the Nintendo Switch
  - reconnect as a bonded device
  - stay connected and exchange controller traffic
- The Switch already recognizes it as a controller.
- `kvmserver` is **not yet integrated** with this daemon.

## Manual startup still required

At the moment the RK board requires several background processes to be started manually before the daemon works:

```bash
/usr/bin/hciattach /dev/ttyS1 any 1500000 flow &
sleep 1
hciconfig hci0 up
/usr/bin/dbus-daemon --system --fork --nopidfile
/usr/libexec/bluetooth/bluetoothd -n -d -P input &
./switchbt-procon --auto-pair --adapter hci0
```

## Still to do

### 1. Create a startup script

Need a script or init service that automatically:

1. attaches the UART Bluetooth chip using `hciattach`
2. brings `hci0` up
3. starts `dbus-daemon`
4. starts `bluetoothd` with `-P input`
5. starts `switchbt-procon`

Suggested placement:

- Buildroot init script under `/etc/init.d/`
- or a project-owned launcher script under `/userdata/server/bin/`

### 2. Persist the final binary in a stable path

Right now testing is happening from:

```text
/userdata/switchbt-procon/switchbt-procon
```

Need a final install path, e.g.:

```text
/userdata/server/bin/switchbt-procon
```

### 3. Update `/etc/main.conf`

BlueZ is still restoring its own default controller class from config, which interferes with the intended gamepad identity.

The following should be updated in `/etc/main.conf`:

```ini
[General]
Class = 0x002508
Name = Pro Controller
```

Then restart `bluetoothd`.

### 4. Integrate with `kvmserver`

The daemon already exposes local IPC over:

```text
/tmp/switchbt-procon.sock
```

`kvmserver` still needs a client for:

- `GET /status`
- `POST /start`
- `POST /stop`
- `POST /input`

So that frontend gamepad `gp` payloads can be forwarded into this daemon.

### 5. Decide production connection policy

Need to choose how production startup should behave:

- always pair (`--auto-pair`)
- always reconnect to last bonded Switch
- or let `kvmserver` decide through IPC

Recommended final behavior:

1. if no bonded Switch is known, start pair mode
2. if a bonded Switch address exists, reconnect automatically

### 6. Store and reuse bonded Switch address

Need a final policy for:

- where the paired Switch address is persisted
- how reconnect mode discovers and uses it
- how to clear/reset pairing state

### 7. Finish protocol completeness

Basic controller behavior works, but some protocol paths are still intentionally incomplete:

- some subcommands are still ignored (for example `0x21`)
- rumble-only packets are ignored
- no IMU support
- no NFC support

These are not blockers for basic button/stick control, but should be tracked.

### 8. Finalize logging policy

High-frequency protocol logs were useful for bring-up, but production should keep only:

- state transition logs
- pairing/reconnect logs
- protocol warnings
- errors

Optional future improvement:

- add a debug flag or env var to re-enable verbose protocol tracing

## Suggested next implementation order

1. create startup script / init integration
2. update `/etc/main.conf`
3. install binary into final path
4. add Unix-socket IPC client inside `kvmserver`
5. forward frontend `gp` payloads into the daemon
6. add automatic reconnect using saved Switch address
