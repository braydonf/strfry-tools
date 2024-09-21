# strfry-tools

This is a collection of programs and plugins for administrating a [strfry](https://github.com/hoytech/strfry) relay for the [Nostr protocol](https://github.com/nostr-protocol/nips).

Included tools:
- `strfry-router` This will help configure a `strfry router` to create relay-to-relay topology.
- `strfry-router-plugin` This is a write policy plugin for `stryfry` to be able to permit specific event authors via streams in a `strfry router`.
- `strfry-write-plugin` This is a standalone write policy plugin.

## Router

This programs configures a `strfry router` to have different relay-to-relay topology. For example, using a WoT based on a set of configured root pubkeys. The `kind 3` (NIP-02) contact/follow list will be retrieved for each root pubkey, each contact will synced `down`, `up` or in `both` directions. This can be extended to different depths. A depth of `1` will sync contacts, a depth of `2` will sync contacts and contacts of contacts, and etcetera. The `kind 10002` (NIP-65) relay list metadata for each contact will be retrieved and used to create router topology. This is the "outbox model" and can help build robust transmission of notes and other stuff. A lightweight client can connect to this relay and have access to all contact pubkeys, without complexity and with efficient use of bandwidth. The `negentropy` protocol extension is used to sync notes for complete and bandwidth efficient syncronization.

### Build & Install

```bash
make router router-plugin
cp strfry-router /usr/local/bin/strfry-router
cp strfry-router-plugin /usr/local/bin/strfry-router-plugin
```
### Configuration

Example configuration (`sample-router.yml`):
```
log-level: "debug"

author-metadata-relays:
  - "wss://domain1.tld"
  - "wss://domian2.tld"

author-wot-users:
  - name: "alice"
	pubkey: "<32-byte-public-key-hex>"
	depth: 1
	dir: down
  - name: "bob"
	pubkey: "<32-byte-public-key-hex>"
	depth: 2
	dir: both
  - name: "carol"
	pubkey: "<32-byte-public-key-hex>"
	depth: 1
	dir: up
```

The `author-metadata-relays` are used to retrieve the `kind 10002` (NIP-65) relay list metadata for each pubkey that has been defined in `author-wot-users`. In this example, the `depth` of `1` will filter, sync and permit only the contacts/follows of "alice" and a depth of `2` will filter, sync and permit contacts and contacts of contacts of "bob", as determined by their `kind 3` list.

### Running

To run the program:
```bash
./strfry-router --conf="</path/to/router.yml>"
```

This will start a daemon process that will create the configuration files necessary to run a `strfry router`. Please see `strfry` [router documentation](https://github.com/hoytech/strfry/blob/master/docs/router.md) for further information about [installing](https://github.com/hoytech/strfry?tab=readme-ov-file#setup) and running the router. The `strfry router` will reload modifications to the config made from this daemon automatically. Similarly the config for the plugin will also met reloaded, thus changes can be made to the topology without restarting the processes.

## Write Policy Plugin

This is a standalone simple plugin for configuring the write policy of a `strfry relay`. This can be with our without running a `strfry router`.

### Build & Install

```bash
make write-plugin
cp strfry-write-plugin /usr/local/bin/strfry-write-plugin
```

### Configuration

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
