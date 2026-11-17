# Icy

Icy is a fork of [Soft Serve](https://github.com/charmbracelet/soft-serve).
Charm has sunset Soft Serve. Thank you to Charm and to everyone who
contributed to it over the years.

A tasty, self-hostable Git server for the command line. 🍦

- Easy to navigate TUI available over SSH
- Clone repos over SSH, HTTP, or Git protocol
- Git LFS support with both HTTP and SSH backends
- Manage repos with SSH
- Create repos on demand with SSH or `git push`
- Browse repos, files and commits with SSH-accessible UI
- Print files over SSH with or without syntax highlighting and line numbers
- Easy access control
  - SSH authentication using public keys
  - Allow/disallow anonymous access
  - Add collaborators with SSH public keys
  - Repos can be public or private
  - User access tokens

## Installation

Icy is a single binary called `icy`. Install it with `go`:

```bash
go install github.com/skunkwerks/icy/cmd/icy@latest
```

Or build it from a checkout:

```bash
go build -o icy ./cmd/icy
```

A [Docker image][docker] is also available.

[docker]: https://github.com/skunkwerks/icy/blob/main/docker.md

## Setting up a server

Make sure `git` is installed, then run `icy serve`. That’s it.

This will create a `data` directory that will store all the repos, ssh keys,
and database.

By default, program configuration is stored within the `data` directory. But,
this can be overridden by setting a custom path to a config file with `ICY_CONFIG_LOCATION`
that is pre-created. If a config file pointed to by `ICY_CONFIG_LOCATION`,
the default location within the `data` dir is used for generating a default config.

To change the default data path use `ICY_DATA_PATH` environment variable.

```sh
ICY_DATA_PATH=/var/lib/icy icy serve
```

When you run Icy for the first time, make sure you have the
`ICY_INITIAL_ADMIN_KEYS` environment variable is set to your ssh
authorized key. Any added key to this variable will be treated as admin with
full privileges.

Using this environment variable, Icy will create a new `admin` user that
has full privileges. You can rename and change the user settings later.

If you'd like a repository to exist the moment the server finishes booting,
with no human logging in to run `repo create`, set `ICY_DEFAULT_REPO`
to a repository name. This is useful for GitOps tools like ArgoCD that need a
git remote to point at as part of their own bootstrap (e.g. from a Helm chart
or Terraform apply):

```sh
ICY_INITIAL_ADMIN_KEYS="$(cat ~/.ssh/id_ed25519.pub)" \
ICY_DEFAULT_REPO=gitops \
  icy serve
```

The repository is created empty and public, exactly as if a human had run
`repo create gitops` with no flags; only its *existence* is guaranteed on
boot, not its reachability, which is still governed entirely by the
`anon-access`/`allow-keyless` settings described below. Booting again against
the same data directory is a no-op if the repository already exists.

Check out [Systemd][systemd] on how to run Icy as a service using
Systemd. Icy packages in our Apt/Yum repositories come with Systemd
service units.

[systemd]: https://github.com/skunkwerks/icy/blob/main/systemd.md

### Server Configuration

Once you start the server for the first time, the settings will be in
`config.yaml` under your data directory. The default `config.yaml` is
self-explanatory and will look like this:

```yaml
# Icy Server configurations

# The name of the server.
# This is the name that will be displayed in the UI.
name: "Icy"

# Log format to use. Valid values are "json", "logfmt", and "text".
log_format: "text"

# The SSH server configuration.
ssh:
  # The address on which the SSH server will listen.
  listen_addr: ":23231"

  # The public URL of the SSH server.
  # This is the address that will be used to clone repositories.
  public_url: "ssh://localhost:23231"

  # The path to the SSH server's private key.
  key_path: "ssh/icy_host"

  # The path to the SSH server's client private key.
  # This key will be used to authenticate the server to make git requests to
  # ssh remotes.
  client_key_path: "ssh/icy_client"

  # How the server's ssh client verifies host keys of ssh remotes it imports
  # or mirrors from, checked against <data_path>/ssh/known_hosts.
  # "accept-new" records a host key on first contact and refuses a changed
  # one (needs OpenSSH 7.6 or newer). "yes" refuses any host that is not
  # already in known_hosts; use it with a pre-seeded known_hosts file.
  client_strict_host_key_checking: "accept-new"

  # The ssh hosts that repositories may be imported from or mirrored. The
  # client key above is presented to every host a user imports from, so list
  # only hosts you trust that key with. Entries are hostnames, IP addresses,
  # or CIDR ranges, each with an optional port. When the list is empty, only
  # server admins may import from or mirror ssh remotes.
  # CIDR entries are checked against DNS only at import or sync time and ssh
  # resolves the name again, so prefer hostnames for anything you do not
  # control. The list is matched against the name given to ssh; your
  # ssh_config still applies to it.
  client_allowed_hosts: []
    # - "git.internal.example"
    # - "gitlab.corp:2222"
    # - "10.0.0.0/8"

  # The maximum number of seconds a connection can take.
  # A value of 0 means no timeout.
  max_timeout: 0

  # The number of seconds a connection can be idle before it is closed.
  idle_timeout: 120

# The Git daemon configuration.
git:
  # The address on which the Git daemon will listen.
  listen_addr: ":9418"

  # The maximum number of seconds a connection can take.
  # A value of 0 means no timeout.
  max_timeout: 0

  # The number of seconds a connection can be idle before it is closed.
  idle_timeout: 3

  # The maximum number of concurrent connections.
  max_connections: 32

# The HTTP server configuration.
http:
  # The address on which the HTTP server will listen.
  listen_addr: ":23232"

  # The path to the TLS private key.
  tls_key_path: ""

  # The path to the TLS certificate.
  tls_cert_path: ""

  # The public URL of the HTTP server.
  # This is the address that will be used to clone repositories.
  # Make sure to use https:// if you are using TLS.
  public_url: "http://localhost:23232"

  # The cross-origin request security options
  cors:
    # The allowed cross-origin headers
    allowed_headers:
      - "Accept"
      - "Accept-Language"
      - "Content-Language"
      - "Content-Type"
      - "Origin"
      - "X-Requested-With"
      - "User-Agent"
      - "Authorization"
      - "Access-Control-Request-Method"
      - "Access-Control-Allow-Origin"

    # The allowed cross-origin URLs
    allowed_origins:
      - "http://localhost:23232" # always allowed
      # - "https://example.com"

    # The allowed cross-origin methods
    allowed_methods:
      - "GET"
      - "HEAD"
      - "POST"
      - "PUT"
      - "OPTIONS"

# The database configuration.
db:
  # The database driver to use.
  # Valid values are "sqlite" and "postgres".
  driver: "sqlite"
  # The database data source name.
  # This is driver specific and can be a file path or connection string.
  # Make sure foreign key support is enabled when using SQLite.
  data_source: "icy.db?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"

# Git LFS configuration.
lfs:
  # Enable Git LFS.
  enabled: true
  # Enable Git SSH transfer.
  ssh_enabled: false

# The read-only web UI, served by the HTTP server.
web:
  # Enable the web UI.
  enabled: true
  # Who runs this server, shown on the front page.
  owner: ""
  # A short description, shown on the front page.
  description: ""

# Cron job configuration
jobs:
  mirror_pull: "@every 10m"

# Repository webhook configuration.
webhook:
  # Private or internal networks that webhooks may reach, such as a CI
  # server on the LAN. Entries are CIDR prefixes or single IP addresses.
  allowed_networks: []
  # How many times a delivery is tried before it is given up.
  max_attempts: 5
  # The wait after the first failed attempt. It doubles after each failure.
  retry_backoff: "2m0s"

# The stats server configuration.
stats:
  # The address on which the stats server will listen.
  listen_addr: ":23233"
# Additional admin keys.
#initial_admin_keys:
#  - "ssh-rsa AAAAB3NzaC1yc2..."

# Override the anon-access and allow-keyless settings below (normally
# managed at runtime via the `settings` command and stored in the
# database). If set, these always take precedence over the database, for
# as long as they remain set -- not just on first run. This is intended for
# scripted, non-production bootstrapping (e.g. spinning up a throwaway
# server for local dev tooling), not for production use.
# With state_source "config" they are written to the database instead.
#anon_access: "admin-access"
#allow_keyless: true

# Repository name to create on boot if it does not already exist.
# Leave empty to disable.
#default_repo: ""

# Where users, repositories and access settings come from: "db" or
# "config". See "Users and Repositories in config.yaml" below.
#state_source: "db"
```

You can also use environment variables, to override these settings. All server
settings environment variables start with `ICY_` followed by the setting
name all in uppercase. Here are some examples:

- `ICY_NAME`: The name of the server that will appear in the TUI
- `ICY_SSH_LISTEN_ADDR`: SSH listen address
- `ICY_SSH_KEY_PATH`: SSH host key-pair path
- `ICY_HTTP_LISTEN_ADDR`: HTTP listen address
- `ICY_HTTP_PUBLIC_URL`: HTTP public URL used for cloning
- `ICY_GIT_MAX_CONNECTIONS`: The number of simultaneous connections to git daemon
- `ICY_ANON_ACCESS`: Overrides the `anon-access` setting (see [Authentication](#authentication))
- `ICY_ALLOW_KEYLESS`: Overrides the `allow-keyless` setting (see [Authentication](#authentication))
- `ICY_DEFAULT_REPO`: Repository name to create on boot if missing
- `ICY_STATE_SOURCE`: Where users, repositories and access come from, `db` or `config` (see [Users and Repositories in config.yaml](#users-and-repositories-in-configyaml))

#### Database Configuration

Icy supports both SQLite and Postgres for its database. Like all other Icy settings, you can change the database _driver_ and _data source_ using either `config.yaml` or environment variables. The default config uses SQLite as the default database driver.

To use Postgres as your database, first create a Icy database:

```sh
psql -h<hostname> -p<port> -U<user> -c 'CREATE DATABASE icy'
```

Then set the database _data source_ to point to your Postgres database. For instance, if you're running Postgres locally, using the default user `postgres` and using a database name `icy`, you would have this config in your config file or environment variable:

```
db:
  driver: "postgres"
  data_source: "postgres://postgres@localhost:5432/icy?sslmode=disable"
```

Environment variables equivalent:

```sh
ICY_DB_DRIVER=postgres \
ICY_DB_DATA_SOURCE="postgres://postgres@localhost:5432/icy?sslmode=disable" \
icy serve
```

You can specify a database connection password in the _data source_ url. For example, `postgres://myuser:dbpass@localhost:5432/my_icy_db`.

#### LFS Configuration

Icy supports both Git LFS [HTTP](https://github.com/git-lfs/git-lfs/blob/main/docs/api/README.md) and [SSH](https://github.com/git-lfs/git-lfs/blob/main/docs/proposals/ssh_adapter.md) protocols out of the box, there is no need to do any extra set up.

Use the `lfs` config section to customize your Git LFS server.

> **Note**: The pure-SSH transfer is disabled by default.

#### Users and Repositories in config.yaml

By default Icy keeps users, repositories and access settings in its database,
and you change them with ssh commands. You can instead list them in
`config.yaml`. This suits tools such as Ansible, which can template the file
and reload the server.

Turn it on with `state_source: config` in `config.yaml`, the
`ICY_STATE_SOURCE=config` environment variable, or
`icy serve --state-source config`. The flag beats the environment variable,
which beats the file. Then add these top-level keys:

```yaml
state_source: config
anon_access: read-only   # no-access, read-only, read-write or admin-access
allow_keyless: true
users:
  - name: alice
    admin: true
    public_keys:
      - "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5... alice@laptop"
  - name: bob
    public_keys:
      - "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5... bob@desk"
repos:
  - name: icy
    project_name: Icy
    description: A Git server
    private: true
    hidden: false
    owner: alice          # defaults to the first admin in users
    collaborators:
      bob: read-write
```

At startup, and each time the server gets `SIGHUP`, Icy reads these keys and
makes the database match them. It adds what is new, updates what has changed
and removes what the file no longer lists. A removed repository is moved to
`repos-removed` in the data directory, not deleted. A reload applies only these
keys. Other settings, such as listen addresses, still need a restart, and the
log says so if they have changed.

Icy checks all of these keys before it changes anything, and applies them in
one database transaction. An unknown key inside `users` or `repos` is an error,
so a typo cannot drop a setting. A file with errors stops the server from
starting. On `SIGHUP`, a file with errors is logged and the server keeps its
current state. Two more checks guard against mistakes:

- The file must list at least one admin with a public key, unless
  `initial_admin_keys` is set, so that you cannot lock yourself out.
- The file must have a `users` or a `repos` key. A `config.yaml` with neither
  would remove every user and repository. Write `users: []` and `repos: []`
  if that is what you want.

`anon_access` and `allow_keyless` follow one rule in both modes. The
environment variable (`ICY_ANON_ACCESS`, `ICY_ALLOW_KEYLESS`) beats
`config.yaml`, which beats the database. With `state_source: db`, a value from
either overrides the database while it is set, and the `settings` command
changes the database underneath. With `state_source: config`, Icy writes the
winning value into the database at startup and on reload. A key left out of
the file means the default: `read-only` and `true`.

While `state_source` is `config`, ssh commands that would change users,
repositories, collaborators or settings are refused. Pushes to repositories in
the file still work. A push to a repository that is not in the file does not
create it. With `state_source: db`, any `users` or `repos` keys in
`config.yaml` are ignored, and the log warns about them.

To check a file before you install it, for example as an Ansible `validate`
step, run `icy admin check-state FILE`. With no `FILE` it checks the server's
own `config.yaml`. To move an existing server over, run
`icy admin export-state`. It prints the current users, repositories and
settings as keys to paste into `config.yaml`.

## Server Access

Icy at its core manages your server authentication and authorization. Authentication verifies the identity of a user, while authorization determines their access rights to a repository.

To manage the server users, access, and repos, you can use the SSH command line interface.

Try `ssh localhost -i ~/.ssh/id_ed25519 -o IdentitiesOnly=yes -p 23231 help` for more info. Make sure
you use your key here.

> **Note** The `IdentitiesOnly` option is used to prevent SSH from using any
> other keys in your `~/.ssh` directory. This is useful when you have multiple
> keys, and you want to use a specific key for Icy.

For ease of use, instead of specifying the key, port, and hostname every time
you SSH into Icy, add your own Icy instance entry to your SSH
config. For instance, to use `ssh icy` instead of typing `ssh localhost -i
~/.ssh/id_ed25519 -o IdentitiesOnly=yes -p 23231`, we can define a `icy` entry in our SSH config
file `~/.ssh/config`.

```conf
Host icy
  HostName localhost
  Port 23231
  IdentityFile ~/.ssh/id_ed25519
  IdentitiesOnly yes
```

Now, we can do `ssh icy` to SSH into Icy. Since `git` is also aware of
this config, you can use `icy` as the hostname for your clone commands.

```sh
git clone ssh://icy/dotfiles
# make changes
# add & commit
git push origin main
```

> **Note** The `-i` and `-o` parts will be omitted in the examples below for brevity. You
> can add your server settings to your sshconfig for quicker access.

### Authentication

Everything that needs authentication is done using SSH. Make sure you have
added an entry for your Icy instance in your `~/.ssh/config` file.

By default, Icy gives read-only permission to anonymous connections to
any of the above protocols. This is controlled by two settings `anon-access`
and `allow-keyless`.

- `anon-access`: Defines the access level for anonymous users. Available
  options are `no-access`, `read-only`, `read-write`, and `admin-access`.
  Default is `read-only`.
- `allow-keyless`: Whether to allow connections that doesn't use keys to pass.
  Setting this to `false` would disable access to SSH keyboard-interactive,
  HTTP, and Git protocol connections. Default is `true`.

```sh
$ ssh -p 23231 localhost settings
Manage server settings

Usage:
  ssh -p 23231 localhost settings [command]

Available Commands:
  allow-keyless Set or get allow keyless access to repositories
  anon-access   Set or get the default access level for anonymous users

Flags:
  -h, --help   help for settings

Use "ssh -p 23231 localhost settings [command] --help" for more information about a command.
```

> **Note** These settings can only be changed by admins.

When `allow-keyless` is disabled, connections that don't use SSH Public Key
authentication will get denied. This means cloning repos over HTTP(s) or git://
will get denied.

Meanwhile, `anon-access` controls the access level granted to connections that
use SSH Public Key authentication but are not registered users. The default
setting for this is `read-only`. This will grant anonymous connections that use
SSH Public Key authentication `read-only` access to public repos.

`anon-access` is also used in combination with `allow-keyless` to determine the
access level for HTTP(s) and git:// clone requests.

#### Local/dev bootstrap

`anon-access` and `allow-keyless` are normally admin-only runtime settings,
which means scripting a fully open server from a cold start would otherwise
require booting with an admin key, waiting for the server to come up, then
SSHing in as that admin to run `settings` commands. For local development
tooling (e.g. standing up a throwaway Git remote for something like Argo CD
to pull from in a dev environment), you can instead set the `ICY_ANON_ACCESS`
and `ICY_ALLOW_KEYLESS` environment variables (or the equivalent
`anon_access`/`allow_keyless` `config.yaml` fields) to grant full,
unauthenticated access from the moment the server starts, with no admin key
and no `settings` command required:

```sh
ICY_ANON_ACCESS=admin-access \
ICY_ALLOW_KEYLESS=true \
  icy serve
```

> **Warning** This grants anyone who can reach the server full admin access
> with no authentication at all. It is intended strictly for local/dev use on
> a machine or network you trust — never expose a server configured this way
> to an untrusted network. The server logs a warning banner on startup
> whenever this combination is active.
>
> Unlike the `settings` command, these overrides always take precedence over
> the database for as long as they're set — not just on first run. If you
> also try to tighten access via `ssh icy settings ...` while an override is
> active, the change is saved but has no effect until the override is
> removed; the `settings` command will warn you when this happens.

#### SSH

Icy doesn't allow duplicate SSH public keys for users. A public key can be associated with one user only. This makes SSH authentication simple and straight forward, add your public key to your Icy user to be able to access Icy.

#### HTTP

You can generate user access tokens through the SSH command line interface. Access tokens can have an optional expiration date. Use your access token as the basic auth user to access your Icy repos through HTTP.

```sh
# Create a user token
ssh -p 23231 localhost token create 'my new token'
ss_1234abc56789012345678901234de246d798fghi

# Or with an expiry date
ssh -p 23231 localhost token create --expires-in 1y 'my other token'
ss_98fghi1234abc56789012345678901234de246d7
```

Now you can access to repos that require `read-write` access.

```sh
git clone http://ss_98fghi1234abc56789012345678901234de246d7@localhost:23232/my-private-repo.git my-private-repo
# Make changes and push
```

### Authorization

Icy offers a simple access control. There are four access levels,
no-access, read-only, read-write, and admin-access.

`admin-access` has full control of the server and can make changes to users and repos.

`read-write` access gets full control of repos.

`read-only` can read public repos.

`no-access` denies access to all repos.

## User Management

Admins can manage users and their keys using the `user` command. Once a user is
created and has access to the server, they can manage their own keys and
settings.

To create a new user simply use `user create`:

```sh
# Create a new user
ssh -p 23231 localhost user create beatrice

# Add user keys
ssh -p 23231 localhost user add-pubkey beatrice ssh-rsa AAAAB3Nz...
ssh -p 23231 localhost user add-pubkey beatrice ssh-ed25519 AAAA...

# Create another user with public key
ssh -p 23231 localhost user create frankie '-k "ssh-ed25519 AAAATzN..."'

# Need help?
ssh -p 23231 localhost user help
```

Once a user is created, they get `read-only` access to public repositories.
They can also create new repositories on the server.

Users can manage their keys using the `pubkey` command:

```sh
# List user keys
ssh -p 23231 localhost pubkey list

# Add key
ssh -p 23231 localhost pubkey add ssh-ed25519 AAAA...

# Wanna change your username?
ssh -p 23231 localhost set-username yolo

# To display user info
ssh -p 23231 localhost info
```

## Repositories

You can manage repositories using the `repo` command.

```sh
# Run repo help
$ ssh -p 23231 localhost repo help
Manage repositories

Usage:
  ssh -p 23231 localhost repo [command]

Aliases:
  repo, repos, repository, repositories

Available Commands:
  blob         Print out the contents of file at path
  branch       Manage repository branches
  collab       Manage collaborators
  create       Create a new repository
  delete       Delete a repository
  description  Set or get the description for a repository
  hide         Hide or unhide a repository
  import       Import a new repository from remote
  info         Get information about a repository
  list         List repositories
  mirror       Set or get a repository mirror property
  private      Set or get a repository private property
  project-name Set or get the project name for a repository
  rename       Rename an existing repository
  tag          Manage repository tags
  tree         Print repository tree at path

Flags:
  -h, --help   help for repo

Use "ssh -p 23231 localhost repo [command] --help" for more information about a command.
```

To use any of the above `repo` commands, a user must be a collaborator in the repository. More on this below.

### Creating Repositories

To create a repository, first make sure you are a registered user. Use the
`repo create <repo>` command to create a new repository:

```sh
# Create a new repository
ssh -p 23231 localhost repo create icecream

# Create a repo with description
ssh -p 23231 localhost repo create icecream '-d "This is an Ice Cream description"'

# ... and project name
ssh -p 23231 localhost repo create icecream '-d "This is an Ice Cream description"' '-n "Ice Cream"'

# I need my repository private!
ssh -p 23231 localhost repo create icecream -p '-d "This is an Ice Cream description"' '-n "Ice Cream"'

# Help?
ssh -p 23231 localhost repo create -h
```

Or you can add your Icy server as a remote to any existing repo, given
you have write access, and push to remote:

```
git remote add origin ssh://localhost:23231/icecream
```

After you’ve added the remote just go ahead and push. If the repo doesn’t exist
on the server it’ll be created.

```
git push origin main
```

### Nested Repositories

Repositories can be nested too:

```sh
# Create a new nested repository
ssh -p 23231 localhost repo create skunkwerks/icecream

# Or ...
git remote add icy ssh://localhost:23231/skunkwerks/icecream
git push icy main
```

### Mirrors

You can also *import* repositories from any public remote. Use the `repo import` command.

```sh
ssh -p 23231 localhost repo import icy https://github.com/skunkwerks/icy
```

Use `--mirror` or `-m` to mark the repository as a *pull* mirror.

### Deleting Repositories

You can delete repositories using the `repo delete <repo>` command.

```sh
ssh -p 23231 localhost repo delete icecream
```

### Renaming Repositories

Use the `repo rename <old> <new>` command to rename existing repositories.

```sh
ssh -p 23231 localhost repo rename icecream vanilla
```

### Repository Collaborators

Sometimes you want to restrict write access to certain repositories. This can
be achieved by adding a collaborator to your repository.

Use the `repo collab <command> <repo>` command to manage repo collaborators.

```sh
# Add collaborator to icy
ssh -p 23231 localhost repo collab add icy frankie

# Add collaborator with a specific access level
ssh -p 23231 localhost repo collab add icy beatrice read-only

# Remove collaborator
ssh -p 23231 localhost repo collab remove icy beatrice

# List collaborators
ssh -p 23231 localhost repo collab list icy
```

### Repository Metadata

You can also change the repo's description, project name, whether it's private,
etc using the `repo <command>` command.

```sh
# Set description for repo
ssh -p 23231 localhost repo description icecream "This is a new description"

# Hide repo from listing
ssh -p 23231 localhost repo hidden icecream true

# List repository info (branches, tags, description, etc)
ssh -p 23231 localhost repo icecream info
```

To make a repository private, use `repo private <repo> [true|false]`. Private
repos can only be accessed by admins and collaborators.

```sh
ssh -p 23231 localhost repo private icecream true
```

### Repository Visibility

A repository has one of three visibilities.

- `public` repositories are listed. Anyone the `anon-access` setting allows
  can read and clone them.
- `unlisted` repositories are not listed anywhere. Anyone who knows the name
  can still read them and clone them over HTTPS.
- `private` repositories can only be read by admins, the owner and
  collaborators. To everyone else a private repository looks exactly like one
  that does not exist.

Visibility is stored in the `private` and `hidden` flags, so the `private` and
`hidden` commands still work. `unlisted` is the same as hidden but not private.

```sh
# Show the visibility
ssh -p 23231 localhost repo visibility icecream

# Change it
ssh -p 23231 localhost repo visibility icecream unlisted

# Set it when creating a repository
ssh -p 23231 localhost repo create icecream --visibility private
```

### Repository Branches & Tags

Use `repo branch` and `repo tag` to list, and delete branches or tags. You can
also use `repo branch default` to set or get the repository default branch.

### Repository Tree

To print a file tree for the project, just use the `repo tree` command along with
the repo name as the SSH command to your Icy server:

```sh
ssh -p 23231 localhost repo tree icy
```

You can also specify the sub-path and a specific reference or branch.

```sh
ssh -p 23231 localhost repo tree icy server/config
ssh -p 23231 localhost repo tree icy main server/config
```

From there, you can print individual files using the `repo blob` command:

```sh
ssh -p 23231 localhost repo blob icy cmd/icy/main.go
```

You can add the `-c` flag to enable syntax coloring and `-l` to print line
numbers:

```sh
ssh -p 23231 localhost repo blob icy cmd/icy/main.go -c -l

```

Use `--raw` to print raw file contents. This is useful for dumping binary data.

### Repository webhooks

A webhook sends an HTTP `POST` to a URL when something happens in a
repository. Repo admins manage them with the `repo webhook` command. Use
`ssh -p 23231 localhost repo webhook help` for the full list of flags.

```sh
# Send push events to a CI server, signed with a secret
ssh -p 23231 localhost repo webhook create icy https://ci.example.com/hook \
  -e push -e branch_tag_create --secret 's3cret'

# List webhooks and their IDs
ssh -p 23231 localhost repo webhook list icy

# Change the URL or events of webhook 1, or switch it off
ssh -p 23231 localhost repo webhook update icy 1 --url https://ci2.example.com/hook
ssh -p 23231 localhost repo webhook update icy 1 --active=false
```

#### Events

Pass one or more of these to `-e`:

| Event                          | Sent when                                       |
| ------------------------------ | ----------------------------------------------- |
| `push`                         | commits are pushed to a branch or tag           |
| `branch_tag_create`            | a branch or tag is created                      |
| `branch_tag_delete`            | a branch or tag is deleted                      |
| `collaborator`                 | a collaborator is added or removed              |
| `repository`                   | the repository is created, deleted, or renamed  |
| `repository_visibility_change` | the repository is made private or public        |

A push that creates a branch sends both `branch_tag_create` and `push`.

#### Payload

The body is JSON by default. Use `--content-type form` to get it as
`application/x-www-form-urlencoded` instead. Every event has `event`,
`repository`, and `sender`. A push event adds these fields:

- `ref` is the full name of the ref that moved, such as `refs/heads/main`.
- `before` is the commit the ref pointed at before the push. It is all
  zeros when the push created the ref.
- `after` is the commit the ref points at now. Build this one.
- `commits` lists up to 20 of the pushed commits, newest first.

A trimmed push payload looks like this:

```json
{
  "event": "push",
  "ref": "refs/heads/main",
  "before": "0f3c1b5a…",
  "after": "9a8e47d2…",
  "repository": {
    "id": 3,
    "name": "icy",
    "private": true,
    "default_branch": "main",
    "owner": { "id": 1, "username": "admin" },
    "http_url": "https://git.example.com/icy.git",
    "ssh_url": "ssh://git.example.com:23231/icy.git",
    "git_url": "git://git.example.com/icy.git"
  },
  "sender": { "id": 2, "username": "frankie" },
  "commits": [
    {
      "id": "9a8e47d2…",
      "title": "Fix the build",
      "message": "Fix the build\n",
      "author": { "name": "Frankie", "email": "frankie@example.com", "date": "2026-11-17T10:04:27Z" },
      "committer": { "name": "Frankie", "email": "frankie@example.com", "date": "2026-11-17T10:04:27Z" },
      "timestamp": "2026-11-17T10:04:27Z"
    }
  ]
}
```

#### Headers

| Header            | Value                                                       |
| ----------------- | ----------------------------------------------------------- |
| `Content-Type`    | `application/json` or `application/x-www-form-urlencoded`   |
| `User-Agent`      | `Icy/<version>`                                             |
| `X-Icy-Event`     | the event name, such as `push`                              |
| `X-Icy-Delivery`  | a UUID for this delivery. Retries send the same UUID.       |
| `X-Icy-Signature` | `sha256=` and the hex HMAC of the body. Sent only with a secret. |

A receiver can drop a delivery whose `X-Icy-Delivery` it has seen before.
This stops a retry from starting a second build.

#### Checking the signature

When the webhook has a secret, Icy signs the raw request body with
HMAC-SHA256 and that secret. Compute the same HMAC over the bytes you
received, before any JSON parsing, and compare it with the header. Reject
the request if they differ.

In shell, with the body saved to `body.json` and the header value in
`HTTP_X_ICY_SIGNATURE`, as a CGI script gets it:

```sh
want="sha256=$(openssl dgst -sha256 -hmac "$WEBHOOK_SECRET" -r body.json | cut -d' ' -f1)"
[ "$want" = "$HTTP_X_ICY_SIGNATURE" ] || { echo "bad signature" >&2; exit 1; }
```

In Go:

```go
import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// validSignature reports whether header is the X-Icy-Signature of body.
func validSignature(body []byte, header, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(header), []byte(want))
}
```

`hmac.Equal` takes the same time for every input, so it does not leak how
much of the signature matched.

#### Delivery, retries, and the delivery log

Icy does not send webhooks during the push. The git hook writes each
delivery to a queue in the database and returns at once. The `icy serve`
process sends queued deliveries within a second or so. A slow or broken
receiver never holds up a push.

A delivery succeeds when the receiver answers with a `2xx` status. Any
other status, a timeout, or a connection error counts as a failure. Each
attempt times out after 30 seconds. After a failure Icy waits and tries
again, and the wait doubles each time. With the defaults it tries five
times, waiting 2, 4, 8, and 16 minutes, so it gives up about 30 minutes
after the push. One failing webhook does not delay the others.

The queue lives in the database, so deliveries survive a restart of the
server. A delivery that was in flight when the server stopped is sent again
when it starts.

Change the retry policy in `config.yaml`, or with `ICY_WEBHOOK_MAX_ATTEMPTS`
and `ICY_WEBHOOK_RETRY_BACKOFF`:

```yaml
webhook:
  max_attempts: 5
  retry_backoff: "2m"
```

Every attempt is kept in the delivery log. Only repo admins can read it,
because it has the full request and response.

```sh
# List deliveries of webhook 1. ✅ is a 2xx answer, ❌ is a failure,
# and ⏳ is still queued or waiting to retry.
ssh -p 23231 localhost repo webhook deliveries list icy 1

# Show the request and response of one attempt
ssh -p 23231 localhost repo webhook deliveries get icy 1 <delivery-id>

# Queue a recorded delivery to be sent again, with a new delivery ID
ssh -p 23231 localhost repo webhook deliveries redeliver icy 1 <delivery-id>
```

The first attempt is logged under its `X-Icy-Delivery` UUID. Each retry
gets its own log entry, and its request headers show the shared
`X-Icy-Delivery`.

#### Webhooks to a private network

Icy refuses webhook URLs that point at private, loopback, link-local,
CGNAT, or other internal addresses. This stops a repo admin from using a
webhook to reach hosts inside your network. It checks the URL when the
webhook is created or updated, and again for the address that it connects
to on each attempt.

If your CI server is on the LAN or in a jail, list its address in
`webhook.allowed_networks`. Entries are CIDR prefixes or single IP
addresses:

```yaml
webhook:
  allowed_networks:
    - "10.0.10.0/24"
    - "192.168.1.20"
```

Or use `ICY_WEBHOOK_ALLOWED_NETWORKS="10.0.10.0/24,192.168.1.20"`.

Hostnames are not accepted in the list. A webhook URL may still use a
hostname, but only if every address it resolves to is public or in the
list. Icy connects to the address that it checked, so a DNS change after
the check cannot send the request somewhere else. Names such as
`localhost` are always refused, so write `http://127.0.0.1:8080/` if the
receiver is on the same host. Keep the list as small as you can. Every
repo admin can send requests to anything in it.

#### Example: build on push

This example runs a CI build on every push to `main` of a private repo
called `app`. The CI server is at `10.0.10.5` on the LAN.

Allow the CI server and restart Icy:

```yaml
webhook:
  allowed_networks:
    - "10.0.10.5"
```

Create a user for CI and give it read-only access to the repo. It can then
clone `app` but not push to it.

```sh
ssh -p 23231 localhost user create ci '-k "ssh-ed25519 AAAA... ci@ci"'
ssh -p 23231 localhost repo collab add app ci read-only
```

As the `ci` user, create an access token for cloning over HTTP:

```sh
ssh -i ~/.ssh/ci_ed25519 -p 23231 git.example.com token create 'ci clone'
```

Store the token and a webhook secret on the CI server. Then add the
webhook:

```sh
ssh -p 23231 localhost repo webhook create app http://10.0.10.5:8080/hook \
  -e push --secret "$WEBHOOK_SECRET"
```

On the CI server, a small receiver checks the signature, answers at once,
and starts the build in the background. It answers before the build ends
so that the attempt does not time out.

```go
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
)

func main() {
	secret := os.Getenv("WEBHOOK_SECRET")
	http.HandleFunc("/hook", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil || !validSignature(body, r.Header.Get("X-Icy-Signature"), secret) {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("X-Icy-Event") != "push" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		var p struct {
			Ref        string `json:"ref"`
			After      string `json:"after"`
			Repository struct {
				Name string `json:"name"`
			} `json:"repository"`
		}
		if err := json.Unmarshal(body, &p); err != nil {
			http.Error(w, "bad payload", http.StatusBadRequest)
			return
		}
		if p.Ref == "refs/heads/main" {
			cmd := exec.Command("./build.sh", p.Repository.Name, p.After, r.Header.Get("X-Icy-Delivery"))
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			if err := cmd.Start(); err == nil {
				go cmd.Wait()
			}
		}
		w.WriteHeader(http.StatusAccepted)
	})
	http.ListenAndServe(":8080", nil)
}
```

`validSignature` is the function from the section above. `build.sh` clones
the exact commit that was pushed, using the token:

```sh
#!/bin/sh
set -eu
repo="$1" commit="$2" delivery="$3"
dir="/var/ci/$delivery"

# Skip a delivery that we have already built
[ -d "$dir" ] && exit 0

git clone "https://$ICY_CI_TOKEN@git.example.com/$repo.git" "$dir"
cd "$dir"
git checkout --detach "$commit"
make test
```

The token is in the clone URL, so do not print it in build logs. If the
build fails to start, check `repo webhook deliveries list app 1` on Icy. It
shows whether the hook reached the CI server and what the CI server
answered.

## The Icy TUI

Icy TUI is mainly used to browse repos over SSH. You can also use it to
browse local repositories with `icy browse` or running `icy` within a Git
repository.

```sh
ssh localhost -p 23231
```

It's also possible to “link” to a specific repo:

```sh
ssh -p 23231 localhost -t icy
```

You can copy text to your clipboard over SSH. For instance, you can press
<kbd>c</kbd> on the highlighted repo in the menu to copy the clone command
[^osc52].

[^osc52]:
    Copying over SSH depends on your terminal support of OSC52. Refer to
    [go-osc52](https://github.com/aymanbagabas/go-osc52) for more information.

## The Web UI

The HTTP server also serves a read-only web view of your repositories. It
needs no JavaScript and follows the browser's light or dark theme.

- `/` shows the server name, owner and description, and the public
  repositories.
- `/<repo>` shows the README, the latest commits and the clone URLs.
- `/<repo>/tree/<path>`, `/<repo>/log` and `/<repo>/refs` browse files,
  history, branches and tags. Add `?h=<branch, tag or commit>` to look at
  another revision.
- `/<repo>/blob/<path>` shows a file with syntax highlighting.
  `/<repo>/raw/<path>` downloads it.
- `/<repo>/commit/<id>` shows one commit and its diff.
- `/-/` holds the server's own pages, such as sign-in.

### Reserved names

The words `tree`, `blob`, `raw`, `log`, `commit`, `refs` and `-` cannot be a
segment of a new repository name, in any letter case. So `org/tree` and
`docs/Log` are refused, but `org/trees` and `changelog` are fine. This is what
lets a URL say where the repository name ends.

A repository created before this rule keeps working over SSH, git over HTTP
and in the TUI. The web UI does not show it, and the server logs a warning
for it at startup. Rename it to show it in the web UI.

Git requests win over web pages. A file whose path ends like a git request,
such as `HEAD` or `info/refs`, cannot be shown in the web UI.

Set `web.owner` and `web.description` in `config.yaml` to fill in the front
page. Set `web.enabled: false`, or `ICY_WEB_ENABLED=false`, to turn the web UI
off.

### Signing in

Anyone can browse what the `anon-access` setting lets them read. To see your
private repositories, sign in with SSH:

```sh
ssh -p 23231 localhost web login
# or go straight to a repository
ssh -p 23231 localhost web login icecream
```

This prints a link. It works once and expires after five minutes. Opening it
gives your browser a session cookie that lasts 24 hours and grants your read
access. Use the "Sign out" button to end the session early.

The cookie is HttpOnly and SameSite=Lax. It is marked Secure when the server
uses TLS or `http.public_url` starts with `https://`. Pending sign-in links
and sessions are tied to the running server. A restart cancels unused links
and signs everyone out.

## Hooks

Icy supports git server-side hooks `pre-receive`, `update`,
`post-update`, and `post-receive`. This means you can define your own hooks to
run on repository push events. Hooks can be defined as a per-repository hook,
and/or global hooks that run for all repositories.

You can find per-repository hooks under the repository `hooks` directory.

Globs hooks can be found in your `ICY_DATA_PATH` directory under
`hooks`. Defining global hooks is useful if you want to run CI/CD for example.

Here's an example of sending a message after receiving a push event. Create an
executable file `<data path>/hooks/update`:

```sh
#!/bin/sh
#
# An example hook script to echo information about the push
# and send it to the client.

refname="$1"
oldrev="$2"
newrev="$3"

# Safety check
if [ -z "$GIT_DIR" ]; then
        echo "Don't run this script from the command line." >&2
        echo " (if you want, you could supply GIT_DIR then run" >&2
        echo "  $0 <ref> <oldrev> <newrev>)" >&2
        exit 1
fi

if [ -z "$refname" -o -z "$oldrev" -o -z "$newrev" ]; then
        echo "usage: $0 <ref> <oldrev> <newrev>" >&2
        exit 1
fi

# Check types
# if $newrev is 0000...0000, it's a commit to delete a ref.
zero=$(git hash-object --stdin </dev/null | tr '[0-9a-f]' '0')
if [ "$newrev" = "$zero" ]; then
        newrev_type=delete
else
        newrev_type=$(git cat-file -t $newrev)
fi

echo "Hi from Icy update hook!"
echo
echo "RefName: $refname"
echo "Change Type: $newrev_type"
echo "Old SHA1: $oldrev"
echo "New SHA1: $newrev"

exit 0
```

Now, you should get a message after pushing changes to any repository.

## A note about RSA keys

Unfortunately, due to a shortcoming in Go’s `x/crypto/ssh` package, Icy
does not currently support access via new SSH RSA keys: only the old SHA-1
ones will work.

Until we sort this out you’ll either need an SHA-1 RSA key or a key with
another algorithm, e.g. Ed25519. Not sure what type of keys you have?
You can check with the following:

```sh
$ find ~/.ssh/id_*.pub -exec ssh-keygen -l -f {} \;
```

If you’re curious about the inner workings of this problem have a look at:

- https://github.com/golang/go/issues/37278
- https://go-review.googlesource.com/c/crypto/+/220037
- https://github.com/golang/crypto/pull/197

## Contributing

See [contributing][contribute].

[contribute]: https://github.com/skunkwerks/icy/contribute

## Feedback

Please open an issue at https://github.com/skunkwerks/icy/issues.

## License

[MIT](https://github.com/skunkwerks/icy/raw/main/LICENSE)

---

Icy is a fork of [Soft Serve](https://github.com/charmbracelet/soft-serve) by
[Charm](https://charm.sh).
