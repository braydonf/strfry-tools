package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"math/rand"
	"time"
	"context"
	"encoding/json"
	"sync"

	"github.com/braydonf/strfry-tools"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/v2"
	"github.com/rs/zerolog"
	"github.com/nbd-wtf/go-nostr"
	"github.com/robfig/cron/v3"
	flag "github.com/spf13/pflag"
)

var (
	knf = koanf.New(".")
	crn = cron.New()
	cfg strfry.Config
	log = zerolog.New(os.Stderr).Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).With().Timestamp().Logger()

	routers strfry.Routers
	syncer strfry.SyncConfig
	counter strfry.ConcurrentCounter
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

func getLatestKind(
	ctx context.Context,
	exited chan struct{},
	pubkey string,
	kind int,
	pool *nostr.SimplePool,
	relays []string) *nostr.Event {

	// TODO handle the case that relays is empty.

	filters := []nostr.Filter{{
		Kinds:   []int{kind},
		Authors: []string{pubkey},
		Limit:   1,
	}}

	counter.Wait(strfry.MaxConcurrentReqs)
	counter.Begin()
	defer counter.Done()

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	results := pool.SubManyEose(ctx, relays, filters)

	var latest *nostr.Event

	// TODO Get the last result from this request from on disk
	// so that even if there is a network failure the contact
	// would not be removed from the list. If there is a newer
	// version from the relays, then use that one instead.

	outer:
	for {
		select {
		case res := <-results:
			if res.Event == nil {
				break outer
			}

			if latest != nil && res.Event.CreatedAt > latest.CreatedAt {
				latest = res.Event
			} else {
				latest = res.Event
			}
		case <-ctx.Done():
		case <-exited:
			cancel()
			break outer
		}
	}

	return latest
}

func getUserRelayMeta(
	ctx context.Context,
	exited chan struct{},
	pubkey string,
	pool *nostr.SimplePool,
	relays []string) []string {

	meta := getLatestKind(ctx, exited, pubkey, nostr.KindRelayListMetadata, pool, relays)

	results := make([]string, 0)

	if meta != nil {
		for _, tag := range meta.Tags.GetAll([]string{"r"}) {
			if len(tag) < 2 {
				log.Warn().Msg("unexpected r tag")
				continue
			}

			relay := nostr.NormalizeURL(tag[1])

			if len(relay) == 0 {
				continue
			}

			results = append(results, relay)
		}
	} else {
		log.Warn().Str("pubkey", pubkey).Msg("no relay list meta")
	}

	return results
}

func getUserFollows(
	ctx context.Context,
	exited chan struct{},
	pubkey string,
	pool *nostr.SimplePool,
	relays []string) []string {

	follows := getLatestKind(ctx, exited, pubkey, nostr.KindContactList, pool, relays)
	pubkeys := make([]string, 0)

	if follows != nil {
		ptags := follows.Tags.GetAll([]string{"p"})

		for _, tag := range ptags {
			if len(tag) < 2 {
				log.Warn().Msg("unexpected p tag")
				continue
			}

			hex := tag[1]
			if nostr.IsValidPublicKeyHex(hex) {
				pubkeys = append(pubkeys, hex)
			} else {
				log.Warn().Str("invalid pubkey: %s", hex)
			}
		}
	} else {
		log.Warn().Str("pubkey", pubkey).Msg("no follow list")
	}

	return pubkeys
}

func getUsersInfo(
	ctx context.Context,
	exited chan struct{},
	users []strfry.User,
	pool *nostr.SimplePool,
	relays []string) {

	wg := sync.WaitGroup{}
	wg.Add(len(users))

	for _, user := range users {
		log.Info().Str("pubkey", user.PubKey).Int("depth", user.Depth).Msg("requesting info")

		go func() {
			// Gather all of the relays for the user.
			userRelays := make([]string, 0)
			if user.RelayDepth >= 0 {
				userRelays = getUserRelayMeta(ctx, exited, user.PubKey, pool, relays)
			}

			// Gather all of the pubkey contacts for the user.
			var contacts = make([]string, 0)
			if user.Depth >= 0 {
				contacts = getUserFollows(ctx, exited, user.PubKey, pool, relays)
			}

			if len(userRelays) > 0 {
				// Now add this user to the router.
				routers.AddUser(&cfg, &user, userRelays, contacts)

				log.Info().Str("pubkey", user.PubKey).Msg("added to routers config")

				// Add to the sync config.
				syncer.AppendUniqueUser(&strfry.SyncUser{
					Direction: user.Direction,
					PubKey: user.PubKey,
					Relays: userRelays,
				})

				log.Info().Str("pubkey", user.PubKey).Msg("added to sync config")
			}

			// Now keep going for the next depth.
			if user.Depth > 0 {
				nextUsers := make([]strfry.User, 0)

				if user.Direction == "down" || user.Direction == "both" {
					for _, hex := range contacts {
						nextUsers = append(nextUsers, strfry.User{
							PubKey: hex,
							Depth: user.Depth - 1,
							RelayDepth: user.RelayDepth - 1,
							Direction: user.Direction,
						})
					}

				}

				if len(nextUsers) > 0 {
					wg.Add(1)

					go func() {
						getUsersInfo(ctx, exited, nextUsers, pool, relays)
						wg.Done()
					}()
				}
			}

			wg.Done()
		}()
	}

	wg.Wait()
}

func writeConfigFiles() {
	wg := sync.WaitGroup{}
	wg.Add(1)

	go func() {
		defer wg.Done()

		for _, router := range routers.Configs {
			path, err := router.WriteFile()
			if err != nil {
				fmt.Errorf("write error: %s", err)
			} else {
				log.Info().Str("file", path).Msg("wrote router config")
			}
		}

		err := routers.WritePluginConfig(cfg.RouterPluginConfig)
		if err != nil {
			fmt.Errorf("write error: %s", err)
		} else {
			log.Info().Str("file", cfg.RouterPluginConfig).Msg("wrote plugin config")
		}

		err = routers.WriteConfig(cfg.RoutersConfig)
		if err != nil {
			fmt.Errorf("write error: %s", err)
		} else {
			log.Info().Str("file", cfg.RoutersConfig).Msg("wrote routers config")
		}

		syncConf, err := json.MarshalIndent(syncer, "", "  ")
		if err != nil {
			fmt.Errorf("marshal error: %s", err)
		} else {
			if err := os.WriteFile(cfg.SyncConfig, syncConf, 0644); err != nil {
				fmt.Errorf("write error: %s", err)
			}
			log.Info().Str("file", cfg.SyncConfig).Msg("wrote sync config")
		}
	}()

	wg.Wait()

	log.Info().Msg("wrote all config files")
}

func main() {
	// Setup the router and plugin.
	routers = strfry.NewRouters()

	// Used to keep the process open and watch for
	// system signals to interrupt the process.
	signals := make(chan os.Signal, 1)
	done := make(chan bool, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	// This is used to signal to go routines to
	// cancel any contexts.
	exited := make(chan struct{})

	go func() {
		sig := <-signals
		fmt.Printf("Received signal: %s\n", sig)
		close(exited)
		done <- true
	}()

	// Setup flags, load config and set log level.
	f := SetupFlags()
	LoadConfig(f, &cfg)
	SetLogLevel(cfg.LogLevel)

	// Now do Nostr requests and other things.
	ctx := context.Background()

	// Set some of the sync config values.
	syncer = strfry.NewSyncConfig()
	syncer.StrFryBin = cfg.StrFryBin
	syncer.LogLevel = cfg.LogLevel
	syncer.StrFryConfig = cfg.StrFryConfig
	syncer.StatusFile = cfg.SyncStatusFile
	syncer.StrFryLogBase = cfg.SyncStrFryLogBase

	// Set the router configuration values.
	routers.Config = &strfry.RoutersConfig{}
	routers.Config.LogLevel = cfg.LogLevel
	routers.Config.ConfigBase = cfg.RouterConfigBase
	routers.Config.StrFryConfig = cfg.StrFryConfig
	routers.Config.StrFryBin = cfg.StrFryBin

	updateUsers := func() {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		pool := nostr.NewSimplePool(ctx)

		for _, relayAddr := range cfg.DiscoveryRelays {
			relay, err := pool.EnsureRelay(relayAddr)

			if err != nil {
				fmt.Errorf("relay error: %s", err)
				continue
			}

			defer relay.Close()
		}

		getUsersInfo(ctx, exited, cfg.Users, pool, cfg.DiscoveryRelays)
		writeConfigFiles()
	}

	updateUsers()

	crn.AddFunc("@every 24h", updateUsers)
	crn.Start()

	<-done

	crn.Stop()
}

func SetLogLevel(lvl string) {
	if lvl == "panic" {
		zerolog.SetGlobalLevel(zerolog.PanicLevel)
	} else if (lvl == "fatal") {
		zerolog.SetGlobalLevel(zerolog.FatalLevel)
	} else if (lvl == "error") {
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	} else if (lvl == "warn") {
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	} else if (lvl == "info") {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	} else if (lvl == "debug") {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else if (lvl == "trace") {
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
}

func SetupFlags() *flag.FlagSet {
	f := flag.NewFlagSet("conf", flag.ContinueOnError)
	f.Usage = func() {
		fmt.Println(f.FlagUsages())
		os.Exit(0)
	}

	f.StringSlice(
		"conf",
		[]string{"config.yml"},
		"path to one or more .yml router config files")

	f.Parse(os.Args[1:])

	return f
}

func LoadConfig(f *flag.FlagSet, cfg *strfry.Config) {
	// Load the config files provided in the commandline.
	filepaths, _ := f.GetStringSlice("conf")
	for _, c := range filepaths {
		if err := knf.Load(file.Provider(c), yaml.Parser()); err != nil {
			log.Fatal().Err(err).Msg("error loading file: %v")
		}
	}

	// Override command over the config file.
	if err := knf.Load(posflag.Provider(f, ".", knf), nil); err != nil {
		log.Fatal().Err(err).Msg("error loading config: %v")
	}

	// Get the settings and set the log level.
	knf.Unmarshal("", &cfg)
}
