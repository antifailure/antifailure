# A real Secret Service, so the Linux keyring is tested against the thing it
# talks to rather than against our idea of it.
#
# There is no keyring daemon on a macOS development machine and none on a CI
# runner by default, so without this the Linux implementation would be code
# nobody had ever executed. Everything here is the real article: a real session
# bus, the real gnome-keyring daemon, and the real secret-tool client. Nothing
# in this image stands in for anything.
FROM golang:1.25-bookworm

# libsecret-tools ships secret-tool. gnome-keyring is the daemon that answers
# it. dbus-x11 brings dbus-run-session, which gives the daemon a session bus
# without an X server or a login session, which is what makes this runnable in
# a container at all.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      libsecret-tools gnome-keyring dbus-x11 \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY run.sh /run.sh
ENTRYPOINT ["/run.sh"]
