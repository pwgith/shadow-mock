# 15. Running in Docker

Every release is also a container image, built for `linux/amd64` and `linux/arm64`:

```
ghcr.io/pwgith/shadow-mock
```

| Tag | What it is |
|---|---|
| `0.2.0` | That release. Pin this in production |
| `0.2` | The newest patch of that minor version |
| `latest` | The newest stable release (never a pre-release) |
| `edge` | A build of the latest `main`. Replaced on every merge |

## Try it

```bash
docker run --rm -p 8080:8080 ghcr.io/pwgith/shadow-mock:latest
```

The server is on `http://localhost:8080`. The admin API is at `/__admin` on the same port.

## What is in the image

* A single static binary, `/shadowmock`, on a minimal base with no shell and no package manager.
* It runs as a non-root user.
* `SHADOWMOCK_ROOT=/data` is the folder for `mappings/`, `__files/` and saved sessions. It is a
  volume, so mount one to keep your data: `-v shadowmock-data:/data`. To start with mappings of
  your own, mount them: `-v "$PWD/mappings:/data/mappings:ro"`.
* `SHADOWMOCK_LISTEN_MOCK=:8080` is the mock port. Any flag or `SHADOWMOCK_*` variable from the
  [configuration page](11-configuration.md) works as it does outside a container.
* A health check. Docker runs `shadowmock -healthcheck`, which asks the server for `/__health` on
  the mock port and exits 0 or 1. The container shows as `healthy` once it answers.

## A shared server with Compose

The repository has a working example in [`deploy/`](../../deploy): a `docker-compose.yml` and a
`shadowmock.yaml` that turn on server mode, admin tokens, a separate admin port, saved sessions
and sensible quotas.

1. Copy the `deploy` folder to your server.
2. Make a `.env` file next to the compose file with two long random tokens:

   ```
   SHADOWMOCK_OPS_TOKEN=<at least 16 random characters>
   SHADOWMOCK_CI_TOKEN=<at least 16 random characters>
   ```

3. In `shadowmock.yaml`, set `admin.allowedHosts` and `server.publicBaseUrl` to the name your
   users will use.
4. Start it:

   ```bash
   docker compose up -d
   ```

Server mode refuses to start without admin tokens, so a missing token stops the container and the
reason is in `docker compose logs`.

Things to know:

* **Keep port 9090 private.** It carries the admin API. Publish only 8080 to the network, or put a
  firewall in front of 9090.
* **Terminate TLS in front.** The server speaks plain HTTP. Put a reverse proxy or load balancer
  in front, then set `server.clientIp` so rate limits and the allowlist see real client addresses
  ([page 12](12-shared-server.md)).
* **Keep the health check's port in step.** The check reads the mock port from
  `SHADOWMOCK_LISTEN_MOCK`, not from the config file, so if you change `listen.mock` in the file,
  change the variable to match.
* **The file system is read-only** in the example. Only `/data` is written to.

## Build the image yourself

```bash
docker build --build-arg VERSION=0.2.0 -t shadow-mock .
```

Without `VERSION` the server reports `0.0.0-dev`.
