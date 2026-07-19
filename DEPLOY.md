# Deploying this fork from source

This fork is developed by building from source on the server. Because the
frontend (`internal/web/dist/`) is git-ignored and embedded into the binary at
compile time, a plain `git clone` does **not** contain it — the build needs the
prebuilt `dist/` shipped alongside the source.

## Build the deploy tarball (on your dev machine)

After `make build` / `npm run build` has populated `internal/web/dist/`:

```bash
git archive --format=tar -o /tmp/3x-ui-install.next.tar HEAD
tar -rf /tmp/3x-ui-install.next.tar internal/web/dist
gzip -f /tmp/3x-ui-install.next.tar
# upload /tmp/3x-ui-install.next.tar.gz to the server
```

## Deploy on the server

Upload the tarball, then:

```bash
cd /root/3x-ui-ssh
tar xzf /root/3x-ui-install.next.tar.gz -C /root/3x-ui-ssh
export PATH=$PATH:/usr/local/go/bin

# First deploy — provide the secret key ONCE so it gets persisted:
XUI_SECRET_KEY=<your-fixed-key> ./deploy.sh

# Every deploy after that — the key is already persisted, nothing to export:
./deploy.sh
```

`deploy.sh` builds (`CGO_ENABLED=1 go build`), persists the key, and restarts.

## XUI_SECRET_KEY persistence

The panel encrypts stored SSH credentials with `XUI_SECRET_KEY`. On start it
auto-loads environment variables from `/etc/default/x-ui` (also `/etc/conf.d/x-ui`,
`/etc/sysconfig/x-ui`) **without overriding** anything already in the shell
environment. `deploy.sh` writes the key into `/etc/default/x-ui` (mode `600`) the
first time it is missing, so you never have to `export XUI_SECRET_KEY` again.

> **Never change the key on an existing install.** A different key makes every
> already-stored SSH credential undecryptable. `deploy.sh` therefore never
> generates or overwrites the key — it only writes the value you supply when the
> env file has none. To set it up manually instead of via `deploy.sh`:
>
> ```bash
> echo 'XUI_SECRET_KEY=<your-key>' | sudo tee -a /etc/default/x-ui
> sudo chmod 600 /etc/default/x-ui
> ```

## Other environment variables

Any variable the panel supports (`XUI_DB_TYPE`, `XUI_LOG_LEVEL`, `XUI_PORT`, …)
can go in the same `/etc/default/x-ui` file, one `KEY=VALUE` per line, and will
be loaded on start.
