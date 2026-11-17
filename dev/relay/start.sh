#!/bin/sh
# Minimal SSH relay for the Kindle reverse tunnel (see dev/README.md). Key login only,
# TCP forwarding on so the Kindle can forward a port back to its own sshd.
apk add --no-cache openssh-server >/dev/null
ssh-keygen -A >/dev/null
id kindle >/dev/null 2>&1 || adduser -D -s /bin/sh kindle
sed -i 's/^kindle:!:/kindle:*:/' /etc/shadow
mkdir -p /home/kindle/.ssh
cat /relay/authorized_keys /relay/kindle_tunnel.pub 2>/dev/null > /home/kindle/.ssh/authorized_keys
chown -R kindle:kindle /home/kindle/.ssh; chmod 700 /home/kindle/.ssh; chmod 600 /home/kindle/.ssh/authorized_keys
cat > /etc/ssh/sshd_config <<CFG
Port 22
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin no
AllowTcpForwarding yes
GatewayPorts no
ClientAliveInterval 15
ClientAliveCountMax 4
AuthorizedKeysFile .ssh/authorized_keys
CFG
exec /usr/sbin/sshd -D -e
