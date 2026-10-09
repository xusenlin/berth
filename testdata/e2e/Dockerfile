# A Linux server for Berth's end-to-end tests: systemd, sshd, and two
# logins (root for system services, deploy for user services).
FROM debian:bookworm
ENV container=docker
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      systemd systemd-sysv dbus dbus-user-session openssh-server sudo curl ca-certificates libpam-systemd \
    && rm -rf /var/lib/apt/lists/* \
    && rm -f /etc/systemd/system/getty.target.wants/* \
    && systemctl enable ssh \
    && echo 'root:berth' | chpasswd \
    && useradd -m -s /bin/bash deploy && echo 'deploy:berth' | chpasswd && usermod -aG sudo deploy \
    && sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config \
    && sed -i 's/^#\?PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
