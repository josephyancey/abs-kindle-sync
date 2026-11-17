# Development setup

These are the tools used to explore a Kindle over SSH. The app doesn't need any of them.

## SSH

Install [USBNetLite](https://github.com/notmarek/kindle-usbnetlite) (see `docs/findings.md` if MRPI fails with `xzdec: Permission denied`). Put your public key in `/mnt/us/usbnetlite/etc/dropbear/authorized_keys`, then toggle USBNetwork on from KUAL.

## Keeping it reachable: `kual-abs-test`

Copy `dev/kual-abs-test/` to `/mnt/us/extensions/abstest/`. KUAL then shows an **ABS Test** menu with two items.

**Start keep-alive** does the following:
- Keeps the Kindle awake (`preventScreenSaver`).
- Turns Wi-Fi power saving off.
- Pings the gateway.
- Re-adds the SSH firewall rule whenever the Kindle drops it.
- If `/mnt/us/abs-test/tunnel.conf` and `/mnt/us/abs-test/tunnel_key` exist, holds a reverse tunnel to a relay.

**Stop keep-alive** undoes all of that.

## Reverse tunnel

Incoming SSH over Wi-Fi was unreliable, while connections the Kindle starts itself work. The fix is to have the Kindle dial out to a relay and forward a port back to its own sshd.

1. **Start the relay** on a machine the Kindle can reach:

   ```sh
   docker run -d --name kindle-relay --restart unless-stopped -p 2222:22 \
     -v "$PWD/dev/relay:/relay" alpine:3.20 sh /relay/start.sh
   ```

   The relay reads `dev/relay/authorized_keys` (your key) and `dev/relay/kindle_tunnel.pub` (the Kindle's key). Neither file is committed.
2. **Make a key for the Kindle.** The Kindle's dropbear has no `dropbearkey`, so create the key elsewhere and convert it:

   ```sh
   ssh-keygen -t ed25519 -N "" -f dev/relay/kindle_tunnel
   docker exec kindle-relay sh -c 'apk add dropbear-convert && dropbearconvert openssh dropbear /relay/kindle_tunnel /relay/kindle_tunnel.dropbear'
   ```

   Copy `kindle_tunnel.dropbear` to `/mnt/us/abs-test/tunnel_key` on the Kindle.
3. **Point the Kindle at the relay.** Copy `dev/tunnel.conf.example` to `/mnt/us/abs-test/tunnel.conf` and set `RELAY_HOST`. Then run **ABS Test → Start keep-alive**.
4. **Connect** through the relay:

   ```sh
   ssh -o ProxyCommand="ssh -p 2222 kindle@RELAY_HOST -W 127.0.0.1:2200" root@kindle
   ```

## Recording events: `capture.sh`

Copy `dev/capture.sh` to the Kindle and run it in the background (`setsid sh capture.sh &`). It records LIPC events, the active app, and reader log lines for 10 minutes into `/mnt/us/abs-test/capture/`.
