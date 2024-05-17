# strfry-writepolicy

## Build & Install

```bash
go get
go build
cp strfry-writepolicy /usr/local/bin/strfry-writepolicy
```

## Configuration

In your `strfry.conf` file, you can add this plugin by including it in the write policy configuration.

```conf
    writePolicy {
        plugin = "/usr/local/bin/strfry-writepolicy"
    }
```

To configure the policies, you can use environment variables or a configuration file at `/etc/strfry-writepolicy.yml`.

Example environment variables:
```conf
export STRFRY_AUTHOR_WHITELIST='<hex-pubkey>, <hex-pubkey>'
```

Example `/etc/strfry-writepolicy.yml` configuration file:
```yaml
author-whitelist:
  - "<32-byte-public-key-hex>"
  - "<32-byte-public-key-hex>"
```
