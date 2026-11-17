# Running Icy as a Systemd Service

Most Linux OSes use Systemd as an init system and service management. You can
use Systemd to manage Icy as a service on your host machine.

Our Icy deb/rpm packages come with Systemd service files pre-packaged.
You can install `icy` from our Apt/Yum repositories. Follow the
[installation instructions](https://github.com/skunkwerks/icy#installation) for
more information.

## Writing a Systemd Service File

> **Note** you can skip this section if you are using our deb/rpm packages or
> installed Icy from our Apt/Yum repositories.

Start by writing a Systemd service file to define how your Icy server
should start.

First, we need to specify where the data should live for our server. Here I
will be choosing `/var/local/lib/icy` to store the server's data. Icy
will look for this path in the `ICY_DATA_PATH` environment
variable.

Make sure this directory exists before proceeding.

```sh
sudo mkdir -p /var/local/lib/icy
```

We will also create a `/etc/icy.conf` file for any extra server settings that we want to override.

```conf
# Config defined here will override the config in /var/local/lib/icy/config.yaml
# Keys defined in `ICY_INITIAL_ADMIN_KEYS` will be merged with
# the `initial_admin_keys` from /var/local/lib/icy/config.yaml.
#
#ICY_GIT_LISTEN_ADDR=:9418
#ICY_HTTP_LISTEN_ADDR=:23232
#ICY_SSH_LISTEN_ADDR=:23231
#ICY_SSH_KEY_PATH=ssh/icy_host_ed25519
#ICY_INITIAL_ADMIN_KEYS='ssh-ed25519 AAAAC3NzaC1lZDI1...'
```

> **Note** Icy stores its server configuration and settings in
> `config.yaml` under its _data path_ directory specified using
> `ICY_DATA_PATH` environment variable.

Now, let's write a new `/etc/systemd/system/icy.service` Systemd service file:

```conf
[Unit]
Description=Icy git server 🍦
Documentation=https://github.com/skunkwerks/icy
Requires=network-online.target
After=network-online.target

[Service]
Type=simple
Restart=always
RestartSec=1
ExecStart=/usr/bin/icy serve
Environment=ICY_DATA_PATH=/var/local/lib/icy
EnvironmentFile=-/etc/icy.conf
WorkingDirectory=/var/local/lib/icy

[Install]
WantedBy=multi-user.target
```

Great, we now have a Systemd service file for Icy. The settings defined
here may vary depending on your specific setup. This assumes that you want to
run Icy as `root`. For more information on Systemd service files, refer
to
[systemd.service](https://www.freedesktop.org/software/systemd/man/systemd.service.html)

## Start Icy on boot

Now that we have our Icy Systemd service file in-place, let's go ahead
and enable and start Icy to run on-boot.

```sh
# Reload systemd daemon
sudo systemctl daemon-reload
# Enable Icy to start on-boot
sudo systemctl enable icy.service
# Start Icy now!!
sudo systemctl start icy.service
```

You can monitor the server logs using `journalctl -u icy.service`. Use
`-f` to _tail_ and follow the logs as they get written.

