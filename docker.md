# Running Icy with Docker

Icy Docker images are available at [ghcr.io/skunkwerks/icy][ghcr].

```sh
docker pull ghcr.io/skunkwerks/icy:latest
```

Here’s how you might run `icy` as a container.  Keep in mind that
repositories are stored in the `/icy` directory, so you’ll likely want
to mount that directory as a volume in order keep your repositories backed up.

```sh
docker run \
  --name=icy \
  --volume /path/to/data:/icy \
  --publish 23231:23231 \
  --publish 23232:23232 \
  --publish 23233:23233 \
  --publish 9418:9418 \
  -e ICY_INITIAL_ADMIN_KEYS="YOUR_ADMIN_KEY_HERE" \
  --restart unless-stopped \
  ghcr.io/skunkwerks/icy:latest
```

Or by using docker-compose:

```yaml
---
version: "3.1"
services:
  icy:
    image: ghcr.io/skunkwerks/icy:latest
    container_name: icy
    volumes:
      - /path/to/data:/icy
    ports:
      - 23231:23231
      - 23232:23232
      - 23233:23233
      - 9418:9418
    environment:
      ICY_INITIAL_ADMIN_KEYS: "YOUR_ADMIN_KEY_HERE"
    restart: unless-stopped
```

[ghcr]: https://github.com/skunkwerks/icy/pkgs/container/icy


> **Warning**
>
> Make sure to run the image without a TTY, i.e.: do not use the `--tty`/`-t`
> flags.


