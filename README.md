# strfry-tools

This is a collection of programs and plugins for administrating a [strfry](https://github.com/hoytech/strfry) relay for the [Nostr protocol](https://github.com/nostr-protocol/nips).

Included tools:
- `strfry-router` This will help configure a `strfry router` to create user specific relay-to-relay network topology.
- `strfry-router-plugin` This is a write policy plugin for `stryfry` to be able to permit specific event authors via streams in a `strfry router`.
- `strfry-write-plugin` This is a standalone write policy plugin.

## Router

This programs configures a `strfry router` to have different user specific relay-to-relay topology. It will configure a network of relays based on a set of configured root pubkeys and their `kind 3` (NIP-02) contact/follow lists and `kind 10002` (NIP-65) relay list metada ("outbox model") and syncing/streaming events for their contacts.

The network can be extended to different depths. A depth of `1` will sync contacts, a depth of `2` will sync contacts and contacts of contacts, and etcetera. Each contact can be streamed/synced `down`, `up` or in `both` directions.

The `negentropy` Nostr protocol extension is used to sync notes for complete and bandwidth efficient syncronization.

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

discovery-relays:
  - "wss://domain1.tld"
  - "wss://domian2.tld"

plugin-down: "/usr/bin/local/strfry-router-plugin"
plugin-config: "/var/local/strfry-router-plugin.json"
router-config: "/var/local/strfry-router.config"

users:
  - name: "alice"
	pubkey: "<32-byte-public-key-hex>"
	depth: 1
	relay-depth: 1
	dir: down
  - name: "bob"
	pubkey: "<32-byte-public-key-hex>"
	depth: 2
	relay-depth: 1
	dir: both
  - name: "carol"
	pubkey: "<32-byte-public-key-hex>"
	depth: 1
	relay-depth: 1
	dir: up
```

- The `discovery-relays` are used to retrieve the `kind 10002` (NIP-65) relay list metadata for each pubkey that has been defined in `users`.
- The `depth` defines how deep the `kind 3` contact/follow list will be navigated. In this example, the `depth` of `1` will filter, sync and permit only the contacts/follows of "alice" and a depth of `2` will filter, sync and permit contacts and contacts of contacts of "bob".
- The `relay-depth` option is the depth that relay list metadata is added to the config, it must be equal or less than the `depth`.
- The `plugin-down` defines the location that the `strfry-router-plugin` executable is located and will be used for the `strfry router` configuration.
- The `plugin-config` defines the configuration for the `strfry router` plugin, and will have a list of author pubkeys that will stream.
- The `router-config` is the location of the `strfry router` configuration.
- The `log-level` defines the verbosity of logs. The options include from less verbose to most: "panic", "fatal", "error", "warn", "info", "debug" and "trace".

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
